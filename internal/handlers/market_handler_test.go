package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/services"
	"backend/internal/testutil"
)

// marketClientSeq gives every market request its own client IP, so the package-level per-IP
// RateLimit budget on /market never makes these tests flaky.
var marketClientSeq atomic.Uint32

func doMarket(t *testing.T, r http.Handler, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	n := marketClientSeq.Add(1)
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = fmt.Sprintf("10.%d.%d.%d:1234", (n>>16)&0xff, (n>>8)&0xff, n&0xff)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeMarketBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body %s)", err, w.Body.String())
	}
	return body
}

func expectKeys(t *testing.T, obj map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing key %q in %v", k, obj)
		}
	}
	if len(obj) != len(keys) {
		t.Errorf("keys = %v, want exactly %v", obj, keys)
	}
}

func expectMarketError(t *testing.T, w *httptest.ResponseRecorder, status int, code, msg string) {
	t.Helper()
	expectStatus(t, w, status)
	var body domain.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (body %s)", err, w.Body.String())
	}
	if body.Code != code || body.Error == "" || (msg != "" && body.Error != msg) {
		t.Errorf("error body = %s, want code %q msg %q", w.Body.String(), code, msg)
	}
}

func newMarketEngine(p *testutil.MarketProvider) http.Handler {
	return newEngine(NewMarketHandler(services.NewMarketService(p)))
}

var fixedTime = time.Date(2026, 10, 25, 9, 41, 0, 0, time.UTC)

func TestMarketHandler_Search(t *testing.T) {
	var gotQuery string
	provider := &testutil.MarketProvider{SearchFn: func(_ context.Context, q string) ([]domain.InstrumentSummary, error) {
		gotQuery = q
		if q == "none" {
			return nil, nil
		}
		return []domain.InstrumentSummary{{
			OrderbookID: "SIM-FJRD", Symbol: "FJRD", Name: "Fjord Robotics", Currency: "SEK",
			Market: "Simulated", LastPrice: 142.37, ChangePercent: 1.25,
		}}, nil
	}}
	r := newMarketEngine(provider)

	t.Run("returns items and null next_cursor", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments?q=fjord", "")
		expectStatus(t, w, http.StatusOK)
		if gotQuery != "fjord" {
			t.Errorf("provider query = %q", gotQuery)
		}
		body := decodeMarketBody(t, w)
		expectKeys(t, body, "items", "next_cursor")
		if body["next_cursor"] != nil {
			t.Errorf("next_cursor = %v, want null", body["next_cursor"])
		}
		items, ok := body["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("items = %v", body["items"])
		}
		item := items[0].(map[string]any)
		expectKeys(t, item, "orderbook_id", "symbol", "name", "currency", "market", "last_price", "change_percent")
		if item["orderbook_id"] != "SIM-FJRD" || item["last_price"] != 142.37 || item["change_percent"] != 1.25 {
			t.Errorf("item = %v", item)
		}
	})

	t.Run("no match is an empty array", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments?q=none", "")
		expectStatus(t, w, http.StatusOK)
		if !strings.Contains(w.Body.String(), `"items":[]`) {
			t.Errorf("body = %s, want items []", w.Body.String())
		}
	})

	t.Run("missing q is 400", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments", "")
		expectMarketError(t, w, http.StatusBadRequest, domain.CodeValidation, "q must not be blank")
	})

	t.Run("blank q is 400", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments?q=%20%20", "")
		expectMarketError(t, w, http.StatusBadRequest, domain.CodeValidation, "q must not be blank")
	})
}

func TestMarketHandler_Quote(t *testing.T) {
	provider := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		if id != "SIM-FJRD" {
			return domain.Quote{}, domain.ErrInstrumentNotFound
		}
		return domain.Quote{
			OrderbookID: id, Symbol: "FJRD", Last: 100, Bid: 99.9, Ask: 100.1,
			ChangePercent: -0.42, Currency: "SEK", UpdatedAt: fixedTime,
		}, nil
	}}
	r := newMarketEngine(provider)

	t.Run("returns quote", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments/SIM-FJRD/quote", "")
		expectStatus(t, w, http.StatusOK)
		body := decodeMarketBody(t, w)
		expectKeys(t, body, "orderbook_id", "symbol", "last", "bid", "ask", "change_percent", "currency", "updated_at")
		if body["last"] != 100.0 || body["bid"] != 99.9 || body["ask"] != 100.1 || body["updated_at"] != "2026-10-25T09:41:00Z" {
			t.Errorf("quote = %v", body)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments/SIM-NOPE/quote", "")
		expectMarketError(t, w, http.StatusNotFound, domain.CodeInstrumentNotFound, "instrument not found")
	})
}

func TestMarketHandler_Details(t *testing.T) {
	pe, mcap := 18.5, 1.2e10
	provider := &testutil.MarketProvider{DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
		return domain.InstrumentDetails{
			OrderbookID: id, Symbol: "FJRD", Name: "Fjord Robotics", Currency: "SEK", Market: "Simulated",
			Sector: "Industrials", Description: "Robots.", LastPrice: 142.37, ChangePercent: 1.25,
			KeyIndicators: domain.KeyIndicators{PERatio: &pe, DividendYieldPct: nil, MarketCap: &mcap},
		}, nil
	}}
	w := doMarket(t, newMarketEngine(provider), http.MethodGet, "/api/v1/market/instruments/SIM-FJRD", "")
	expectStatus(t, w, http.StatusOK)
	body := decodeMarketBody(t, w)
	expectKeys(t, body, "orderbook_id", "symbol", "name", "currency", "market", "sector", "description",
		"last_price", "change_percent", "key_indicators")
	ki, ok := body["key_indicators"].(map[string]any)
	if !ok {
		t.Fatalf("key_indicators = %v", body["key_indicators"])
	}
	expectKeys(t, ki, "pe_ratio", "dividend_yield_pct", "market_cap")
	if ki["pe_ratio"] != 18.5 || ki["dividend_yield_pct"] != nil || ki["market_cap"] != 1.2e10 {
		t.Errorf("key_indicators = %v", ki)
	}
	if !strings.Contains(w.Body.String(), `"dividend_yield_pct":null`) {
		t.Errorf("dividend_yield_pct must serialize as null: %s", w.Body.String())
	}
	if body["description"] != "Robots." {
		t.Errorf("description = %v", body["description"])
	}
}

func TestMarketHandler_Candles(t *testing.T) {
	var gotPeriod domain.CandlePeriod
	provider := &testutil.MarketProvider{CandlesFn: func(_ context.Context, _ string, p domain.CandlePeriod) ([]domain.Candle, error) {
		gotPeriod = p
		return []domain.Candle{{T: fixedTime, O: 10, H: 12, L: 9, C: 11, V: 1500}}, nil
	}}
	r := newMarketEngine(provider)

	t.Run("returns candles", func(t *testing.T) {
		w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments/SIM-FJRD/candles?period=1m", "")
		expectStatus(t, w, http.StatusOK)
		if gotPeriod != domain.Period1M {
			t.Errorf("provider period = %q", gotPeriod)
		}
		body := decodeMarketBody(t, w)
		expectKeys(t, body, "orderbook_id", "period", "candles")
		if body["orderbook_id"] != "SIM-FJRD" || body["period"] != "1m" {
			t.Errorf("body = %v", body)
		}
		candles := body["candles"].([]any)
		if len(candles) != 1 {
			t.Fatalf("candles = %v", candles)
		}
		c := candles[0].(map[string]any)
		expectKeys(t, c, "t", "o", "h", "l", "c", "v")
		if c["t"] != "2026-10-25T09:41:00Z" || c["o"] != 10.0 || c["h"] != 12.0 || c["l"] != 9.0 || c["c"] != 11.0 || c["v"] != 1500.0 {
			t.Errorf("candle = %v", c)
		}
	})

	for _, p := range []string{"5x", ""} {
		t.Run("period "+p+" is 400", func(t *testing.T) {
			w := doMarket(t, r, http.MethodGet, "/api/v1/market/instruments/SIM-FJRD/candles?period="+p, "")
			expectMarketError(t, w, http.StatusBadRequest, domain.CodeValidation, "period must be one of 1d, 1w, 1m, 3m, 1y")
		})
	}

	t.Run("empty result is an empty array", func(t *testing.T) {
		empty := &testutil.MarketProvider{CandlesFn: func(context.Context, string, domain.CandlePeriod) ([]domain.Candle, error) {
			return nil, nil
		}}
		w := doMarket(t, newMarketEngine(empty), http.MethodGet, "/api/v1/market/instruments/SIM-FJRD/candles?period=1d", "")
		expectStatus(t, w, http.StatusOK)
		if !strings.Contains(w.Body.String(), `"candles":[]`) {
			t.Errorf("body = %s, want candles []", w.Body.String())
		}
	})
}

func TestMarketHandler_MapsProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		msg    string
	}{
		{"not found", domain.ErrInstrumentNotFound, http.StatusNotFound, domain.CodeInstrumentNotFound, "instrument not found"},
		{"wrapped not found", fmt.Errorf("lookup: %w", domain.ErrInstrumentNotFound), http.StatusNotFound, domain.CodeInstrumentNotFound, "instrument not found"},
		{"upstream down", fmt.Errorf("avanza: GET https://secret.example/x: status 503: %w", domain.ErrMarketDataUnavailable),
			http.StatusBadGateway, domain.CodeMarketDataUnavailable, "market data unavailable"},
		{"validation", &domain.ValidationError{Msg: "bad input"}, http.StatusBadRequest, domain.CodeValidation, "bad input"},
		{"unexpected", errors.New("db exploded at 10.0.0.1"), http.StatusInternalServerError, domain.CodeInternal, ""},
	}
	routes := []string{
		"/api/v1/market/instruments?q=x",
		"/api/v1/market/instruments/SIM-X",
		"/api/v1/market/instruments/SIM-X/quote",
		"/api/v1/market/instruments/SIM-X/candles?period=1d",
	}
	for _, tc := range cases {
		provider := &testutil.MarketProvider{
			SearchFn: func(context.Context, string) ([]domain.InstrumentSummary, error) { return nil, tc.err },
			QuoteFn:  func(context.Context, string) (domain.Quote, error) { return domain.Quote{}, tc.err },
			DetailsFn: func(context.Context, string) (domain.InstrumentDetails, error) {
				return domain.InstrumentDetails{}, tc.err
			},
			CandlesFn: func(context.Context, string, domain.CandlePeriod) ([]domain.Candle, error) { return nil, tc.err },
		}
		r := newMarketEngine(provider)
		for _, route := range routes {
			t.Run(tc.name+" "+route, func(t *testing.T) {
				w := doMarket(t, r, http.MethodGet, route, "")
				expectMarketError(t, w, tc.status, tc.code, tc.msg)
				if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "10.0.0.1") {
					t.Errorf("upstream details leaked: %s", w.Body.String())
				}
			})
		}
	}
}

func TestMarketHandler_IsRateLimited(t *testing.T) {
	provider := &testutil.MarketProvider{QuoteFn: testutil.FixedQuote(domain.Quote{OrderbookID: "SIM-FJRD"})}
	r := newMarketEngine(provider)
	n := marketClientSeq.Add(1)
	ip := fmt.Sprintf("172.16.%d.%d:1234", (n>>8)&0xff, n&0xff)
	limited := false
	for i := 0; i < 30 && !limited; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/market/instruments/SIM-FJRD/quote", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("expected /market to answer 429 after the burst from one IP")
	}
}
