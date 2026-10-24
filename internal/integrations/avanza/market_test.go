package avanza

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"
)

// fakeAvanza serves canned bodies keyed by "METHOD /path" and records every request it sees.
type fakeAvanza struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	routes   map[string]route
	requests []recordedRequest
}

type route struct {
	status int
	body   string
}

type recordedRequest struct {
	Method      string
	Path        string
	Query       string
	ContentType string
	Body        string
}

func newFakeAvanza(t *testing.T) *fakeAvanza {
	t.Helper()
	f := &fakeAvanza{t: t, routes: map[string]route{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeAvanza) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		Query:       r.URL.RawQuery,
		ContentType: r.Header.Get("Content-Type"),
		Body:        string(body),
	})
	rt, ok := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()

	if !ok {
		http.Error(w, `{"error":"no route in fake"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rt.status)
	_, _ = io.WriteString(w, rt.body)
}

func (f *fakeAvanza) handle(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = route{status: status, body: body}
}

func (f *fakeAvanza) handleFile(method, path, file string) {
	f.handle(method, path, http.StatusOK, readTestdata(f.t, file))
}

func (f *fakeAvanza) seen() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func (f *fakeAvanza) provider() *MarketProvider {
	// The trailing slash checks that NewMarketProvider strips it.
	return NewMarketProvider(f.server.Client(), f.server.URL+"/")
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return string(b)
}

const (
	quotePath   = "/_api/stock-guide/5247/quote"
	detailsPath = "/_api/stock-guide/5247/details"
	guidePath   = "/_api/market-guide/stock/5247"
	chartPath   = "/_api/price-chart/stock/5247"
)

func TestNewMarketProviderBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                        "https://www.avanza.se",
		"  ":                      "https://www.avanza.se",
		"http://localhost:9999/":  "http://localhost:9999",
		"http://localhost:9999//": "http://localhost:9999",
		"https://example.test":    "https://example.test",
	}
	for in, want := range cases {
		if got := NewMarketProvider(nil, in).baseURL; got != want {
			t.Errorf("NewMarketProvider(%q).baseURL = %q, want %q", in, got, want)
		}
	}
	if NewMarketProvider(nil, "").httpClient == nil {
		t.Error("nil httpClient should get a default client")
	}
}

func TestSearchMapsStockHits(t *testing.T) {
	f := newFakeAvanza(t)
	f.handleFile(http.MethodPost, searchPath, "search.json")

	items, err := f.provider().Search(context.Background(), "volvo")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := []domain.InstrumentSummary{
		{OrderbookID: "5269", Symbol: "VOLV B", Name: "Volvo B", Currency: "SEK", Market: "Stockholmsbörsen", LastPrice: 307.7, ChangePercent: -1.12},
		{OrderbookID: "1296604", Symbol: "VOLCAR B", Name: "Volvo Car B", Currency: "SEK", Market: "Stockholmsbörsen", LastPrice: 10.7, ChangePercent: -24.23},
		{OrderbookID: "5268", Symbol: "VOLV A", Name: "Volvo A", Currency: "SEK", Market: "Stockholmsbörsen", LastPrice: 308, ChangePercent: -1.41},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items (%+v), want %d stock hits", len(items), items, len(want))
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("items[%d] = %+v, want %+v", i, items[i], want[i])
		}
	}

	reqs := f.seen()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Path != searchPath {
		t.Errorf("request = %s %s, want POST %s", r.Method, r.Path, searchPath)
	}
	if r.ContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", r.ContentType)
	}
	var sent searchRequestBody
	if err := json.Unmarshal([]byte(r.Body), &sent); err != nil {
		t.Fatalf("decode sent body: %v", err)
	}
	if sent.Query != "volvo" || sent.SearchSessionID == "" || sent.Pagination.Size != searchPageSize {
		t.Errorf("sent body = %+v", sent)
	}
}

func TestSearchChangePercentFormatsAndFallbacks(t *testing.T) {
	f := newFakeAvanza(t)
	f.handle(http.MethodPost, searchPath, http.StatusOK, `{"hits":[
		{"type":"STOCK","title":"Comma AB (COM)","orderBookId":"1","marketPlaceName":"First North",
		 "price":{"last":"1 234,555","currency":"EUR","todayChangePercent":"1,2"}},
		{"type":"STOCK","title":"Number AB","tickerSymbol":"NUM","orderBookId":"2",
		 "price":{"last":12.5,"todayChangePercent":-0.456}},
		{"type":"STOCK","title":"Bare AB","orderBookId":"3","price":{"last":null,"todayChangePercent":"-"}},
		{"type":"STOCK","title":"No Price AB","orderBookId":"4"},
		{"type":"FUND","title":"A Fund","orderBookId":"5"},
		{"type":"STOCK","title":"Missing id"}
	]}`)

	items, err := f.provider().Search(context.Background(), "ab")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	want := []domain.InstrumentSummary{
		{OrderbookID: "1", Symbol: "COM", Name: "Comma AB", Currency: "EUR", Market: "First North", LastPrice: 1234.56, ChangePercent: 1.2},
		{OrderbookID: "2", Symbol: "NUM", Name: "Number AB", Currency: "SEK", LastPrice: 12.5, ChangePercent: -0.46},
		{OrderbookID: "3", Name: "Bare AB", Currency: "SEK"},
		{OrderbookID: "4", Name: "No Price AB", Currency: "SEK"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %+v, want %+v", items, want)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("items[%d] = %+v, want %+v", i, items[i], want[i])
		}
	}
}

func TestSearchCapsAtTwentyAndReturnsEmptySlice(t *testing.T) {
	var hits []string
	for i := range 25 {
		hits = append(hits, fmt.Sprintf(`{"type":"STOCK","title":"S%d (S%d)","orderBookId":"%d"}`, i, i, i))
	}

	f := newFakeAvanza(t)
	f.handle(http.MethodPost, searchPath, http.StatusOK, `{"hits":[`+strings.Join(hits, ",")+`]}`)
	items, err := f.provider().Search(context.Background(), "s")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != maxSearchHits || items[0].OrderbookID != "0" || items[19].OrderbookID != "19" {
		t.Errorf("got %d items (first %q), want the first 20", len(items), items[0].OrderbookID)
	}

	for _, body := range []string{`{"hits":[]}`, `{}`, ``} {
		f.handle(http.MethodPost, searchPath, http.StatusOK, body)
		items, err := f.provider().Search(context.Background(), "nothing")
		if err != nil {
			t.Fatalf("Search(%q body): %v", body, err)
		}
		if items == nil || len(items) != 0 {
			t.Errorf("Search(%q body) = %#v, want empty non-nil slice", body, items)
		}
	}
}

func TestQuoteMapsRecordedPayload(t *testing.T) {
	f := newFakeAvanza(t)
	f.handleFile(http.MethodGet, quotePath, "quote.json")

	q, err := f.provider().Quote(context.Background(), "5247")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}

	want := domain.Quote{
		OrderbookID:   "5247",
		Symbol:        "",
		Last:          412,
		Bid:           411.5,
		Ask:           411.65,
		ChangePercent: 0.72,
		Currency:      "SEK",
		UpdatedAt:     time.UnixMilli(1792771200617).UTC(),
	}
	if q != want {
		t.Errorf("Quote = %+v, want %+v", q, want)
	}
	if q.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt location = %v, want UTC", q.UpdatedAt.Location())
	}

	reqs := f.seen()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != quotePath {
		t.Errorf("requests = %+v, want one GET %s", reqs, quotePath)
	}
}

func TestQuoteRoundsAndFallsBackToLast(t *testing.T) {
	f := newFakeAvanza(t)
	f.handle(http.MethodGet, quotePath, http.StatusOK, `{"last":10.695,"changePercent":-1.005,"timeOfLast":1792769392000}`)

	q, err := f.provider().Quote(context.Background(), "5247")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if q.Last != 10.7 || q.Bid != 10.7 || q.Ask != 10.7 {
		t.Errorf("prices = %v/%v/%v, want 10.7 each", q.Last, q.Bid, q.Ask)
	}
	if !q.UpdatedAt.Equal(time.UnixMilli(1792769392000)) {
		t.Errorf("UpdatedAt = %v, want timeOfLast", q.UpdatedAt)
	}
}

func TestQuoteEmptyPayloadIsNotFound(t *testing.T) {
	for _, body := range []string{``, `null`, `{}`, `{"isRealTime":false}`} {
		f := newFakeAvanza(t)
		f.handle(http.MethodGet, quotePath, http.StatusOK, body)
		_, err := f.provider().Quote(context.Background(), "5247")
		if !errors.Is(err, domain.ErrInstrumentNotFound) {
			t.Errorf("Quote with body %q: err = %v, want ErrInstrumentNotFound", body, err)
		}
	}
}

func TestDetailsMapsRecordedPayloads(t *testing.T) {
	f := newFakeAvanza(t)
	f.handleFile(http.MethodGet, guidePath, "market_guide.json")
	f.handleFile(http.MethodGet, detailsPath, "details.json")

	d, err := f.provider().Details(context.Background(), "5247")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}

	if d.OrderbookID != "5247" || d.Name != "Investor B" || d.Symbol != "INVE B" ||
		d.Currency != "SEK" || d.Market != "Stockholmsbörsen" || d.Sector != "Investmentbolag" {
		t.Errorf("identity fields = %+v", d)
	}
	if !strings.HasPrefix(d.Description, "Investor är ett svenskt investment- och holdingbolag") {
		t.Errorf("Description = %q", d.Description)
	}
	if d.LastPrice != 412 || d.ChangePercent != 0.72 {
		t.Errorf("LastPrice/ChangePercent = %v/%v, want 412/0.72", d.LastPrice, d.ChangePercent)
	}
	assertFloatPtr(t, "pe_ratio", d.KeyIndicators.PERatio, 5.88)
	assertFloatPtr(t, "dividend_yield_pct", d.KeyIndicators.DividendYieldPct, 1.36)
	assertFloatPtr(t, "market_cap", d.KeyIndicators.MarketCap, 1257197898197)

	for _, r := range f.seen() {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.Path)
		}
		if r.Path == quotePath {
			t.Error("quote endpoint called although the market guide has a quote")
		}
	}
}

func TestDetailsFallsBackToQuoteEndpointAndNilIndicators(t *testing.T) {
	f := newFakeAvanza(t)
	f.handle(http.MethodGet, guidePath, http.StatusOK, `{"orderbookId":"5247","name":"Guide Name","listing":{}}`)
	f.handle(http.MethodGet, detailsPath, http.StatusOK, `{"company":{"description":" A company. "},"keyIndicators":{"directYield":0.0325}}`)
	f.handleFile(http.MethodGet, quotePath, "quote.json")

	d, err := f.provider().Details(context.Background(), "5247")
	if err != nil {
		t.Fatalf("Details: %v", err)
	}
	if d.Name != "Guide Name" || d.Symbol != "" || d.Currency != "SEK" || d.Sector != "" || d.Description != "A company." {
		t.Errorf("Details = %+v", d)
	}
	if d.LastPrice != 412 || d.ChangePercent != 0.72 {
		t.Errorf("LastPrice/ChangePercent = %v/%v, want the quote endpoint's 412/0.72", d.LastPrice, d.ChangePercent)
	}
	if d.KeyIndicators.PERatio != nil || d.KeyIndicators.MarketCap != nil {
		t.Errorf("missing indicators should be nil, got %+v", d.KeyIndicators)
	}
	assertFloatPtr(t, "dividend_yield_pct", d.KeyIndicators.DividendYieldPct, 3.25)
}

func TestCandlesMapsRecordedPayload(t *testing.T) {
	f := newFakeAvanza(t)
	f.handleFile(http.MethodGet, chartPath, "chart.json")

	candles, err := f.provider().Candles(context.Background(), "5247", domain.Period1M)
	if err != nil {
		t.Fatalf("Candles: %v", err)
	}
	if len(candles) != 6 {
		t.Fatalf("got %d candles, want 6", len(candles))
	}
	first := domain.Candle{T: time.UnixMilli(1790319600000).UTC(), O: 412.9, H: 415.7, L: 412.85, C: 413.5, V: 354737}
	if candles[0] != first {
		t.Errorf("candles[0] = %+v, want %+v", candles[0], first)
	}
	assertAscending(t, candles)

	reqs := f.seen()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Path != chartPath || reqs[0].Query != "timePeriod=one_month" {
		t.Errorf("requests = %+v, want GET %s?timePeriod=one_month", reqs, chartPath)
	}
}

func TestCandlesPeriodParameter(t *testing.T) {
	want := map[domain.CandlePeriod]string{
		domain.Period1D: "one_day",
		domain.Period1W: "one_week",
		domain.Period1M: "one_month",
		domain.Period3M: "three_months",
		domain.Period1Y: "one_year",
	}
	for _, period := range domain.CandlePeriods {
		f := newFakeAvanza(t)
		f.handle(http.MethodGet, chartPath, http.StatusOK, `{"ohlc":[]}`)
		candles, err := f.provider().Candles(context.Background(), "5247", period)
		if err != nil {
			t.Fatalf("Candles(%s): %v", period, err)
		}
		if candles == nil {
			t.Errorf("Candles(%s) returned nil, want empty slice", period)
		}
		if q := f.seen()[0].Query; q != "timePeriod="+want[period] {
			t.Errorf("Candles(%s) query = %q, want timePeriod=%s", period, q, want[period])
		}
	}

	f := newFakeAvanza(t)
	_, err := f.provider().Candles(context.Background(), "5247", domain.CandlePeriod("5y"))
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("unknown period: err = %v, want *domain.ValidationError", err)
	}
	if len(f.seen()) != 0 {
		t.Error("unknown period should not hit the upstream")
	}
}

func TestCandlesSortsDedupesAndDropsZeroPrices(t *testing.T) {
	f := newFakeAvanza(t)
	f.handle(http.MethodGet, chartPath, http.StatusOK, `{"ohlc":[
		{"timestamp":3000,"open":3,"high":3.5,"low":2.5,"close":3.25,"totalVolumeTraded":30},
		{"timestamp":1000,"open":1,"high":1.5,"low":0.5,"close":1.25,"totalVolumeTraded":10.6},
		{"timestamp":2000,"open":0,"high":0,"low":0,"close":0,"totalVolumeTraded":0},
		{"timestamp":3000,"open":9,"high":9,"low":9,"close":9,"totalVolumeTraded":90},
		{"timestamp":4000,"close":4,"totalVolumeTraded":null},
		{"timestamp":5000,"open":5,"high":5,"low":5,"close":null}
	]}`)

	candles, err := f.provider().Candles(context.Background(), "5247", domain.Period1D)
	if err != nil {
		t.Fatalf("Candles: %v", err)
	}
	want := []domain.Candle{
		{T: time.UnixMilli(1000).UTC(), O: 1, H: 1.5, L: 0.5, C: 1.25, V: 11},
		{T: time.UnixMilli(3000).UTC(), O: 3, H: 3.5, L: 2.5, C: 3.25, V: 30},
		{T: time.UnixMilli(4000).UTC(), O: 4, H: 4, L: 4, C: 4, V: 0},
	}
	if len(candles) != len(want) {
		t.Fatalf("got %+v, want %+v", candles, want)
	}
	for i := range want {
		if candles[i] != want[i] {
			t.Errorf("candles[%d] = %+v, want %+v", i, candles[i], want[i])
		}
	}
}

func TestCandlesMissingSeriesIsEmpty(t *testing.T) {
	f := newFakeAvanza(t)
	f.handle(http.MethodGet, chartPath, http.StatusOK, `{"metadata":{}}`)
	candles, err := f.provider().Candles(context.Background(), "5247", domain.Period1D)
	if err != nil || candles == nil || len(candles) != 0 {
		t.Errorf("Candles = %#v, %v; want empty non-nil slice", candles, err)
	}
}

// call runs one provider method against the fake; each case in the error tests uses it.
type call struct {
	name string
	path string // the route the failure is served on
	prep func(f *fakeAvanza)
	run  func(p *MarketProvider) error
}

func providerCalls() []call {
	ctx := context.Background()
	return []call{
		{
			name: "search", path: "POST " + searchPath,
			run: func(p *MarketProvider) error { _, err := p.Search(ctx, "x"); return err },
		},
		{
			name: "quote", path: "GET " + quotePath,
			run: func(p *MarketProvider) error { _, err := p.Quote(ctx, "5247"); return err },
		},
		{
			name: "details/market guide", path: "GET " + guidePath,
			run: func(p *MarketProvider) error { _, err := p.Details(ctx, "5247"); return err },
		},
		{
			name: "details/details", path: "GET " + detailsPath,
			prep: func(f *fakeAvanza) { f.handleFile(http.MethodGet, guidePath, "market_guide.json") },
			run:  func(p *MarketProvider) error { _, err := p.Details(ctx, "5247"); return err },
		},
		{
			name: "candles", path: "GET " + chartPath,
			run: func(p *MarketProvider) error { _, err := p.Candles(ctx, "5247", domain.Period1W); return err },
		},
	}
}

func serveFailure(f *fakeAvanza, c call, status int, body string) {
	if c.prep != nil {
		c.prep(f)
	}
	method, path, _ := strings.Cut(c.path, " ")
	f.handle(method, path, status, body)
}

func TestNotFoundMapsToErrInstrumentNotFound(t *testing.T) {
	for _, c := range providerCalls() {
		if c.name == "search" {
			continue // search has no id; a 404 there is an upstream failure (covered below)
		}
		t.Run(c.name, func(t *testing.T) {
			f := newFakeAvanza(t)
			serveFailure(f, c, http.StatusNotFound, `{"error":"Orderbook with id 5247 was not found"}`)
			err := c.run(f.provider())
			if !errors.Is(err, domain.ErrInstrumentNotFound) {
				t.Fatalf("err = %v, want ErrInstrumentNotFound", err)
			}
			if errors.Is(err, domain.ErrMarketDataUnavailable) {
				t.Errorf("err = %v must not also be ErrMarketDataUnavailable", err)
			}
		})
	}
}

func TestUpstreamFailuresMapToErrMarketDataUnavailable(t *testing.T) {
	longBody := strings.Repeat("x", 10_000)
	failures := []struct {
		name   string
		status int
		body   string
		cause  string
	}{
		{"500", http.StatusInternalServerError, `{"statusCode":500,"message":"Internal server error"}`, "HTTP 500"},
		{"503 long body", http.StatusServiceUnavailable, longBody, "HTTP 503"},
		{"malformed JSON", http.StatusOK, `{"hits": [ {"type": `, "decode response"},
		{"wrong JSON type", http.StatusOK, `[1,2,3]`, "decode response"},
	}

	for _, c := range providerCalls() {
		for _, fl := range failures {
			t.Run(c.name+"/"+fl.name, func(t *testing.T) {
				f := newFakeAvanza(t)
				serveFailure(f, c, fl.status, fl.body)
				err := c.run(f.provider())
				if !errors.Is(err, domain.ErrMarketDataUnavailable) {
					t.Fatalf("err = %v, want ErrMarketDataUnavailable", err)
				}
				if !strings.Contains(err.Error(), fl.cause) {
					t.Errorf("err = %q, want the cause %q in the message", err, fl.cause)
				}
				if len(err.Error()) > 400 {
					t.Errorf("error message is %d bytes; upstream bodies must be truncated", len(err.Error()))
				}
			})
		}
	}

	t.Run("search 404", func(t *testing.T) {
		f := newFakeAvanza(t)
		f.handle(http.MethodPost, searchPath, http.StatusNotFound, `{}`)
		if _, err := f.provider().Search(context.Background(), "x"); !errors.Is(err, domain.ErrMarketDataUnavailable) {
			t.Errorf("err = %v, want ErrMarketDataUnavailable", err)
		}
	})
}

func TestTransportFailuresMapToErrMarketDataUnavailable(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		base := srv.URL
		srv.Close()

		_, err := NewMarketProvider(nil, base).Quote(context.Background(), "5247")
		if !errors.Is(err, domain.ErrMarketDataUnavailable) {
			t.Fatalf("err = %v, want ErrMarketDataUnavailable", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(release) })

		p := NewMarketProvider(&http.Client{Timeout: 50 * time.Millisecond}, srv.URL)
		_, err := p.Candles(context.Background(), "5247", domain.Period1D)
		if !errors.Is(err, domain.ErrMarketDataUnavailable) {
			t.Fatalf("err = %v, want ErrMarketDataUnavailable", err)
		}
	})

	t.Run("cancelled context keeps its cause", func(t *testing.T) {
		f := newFakeAvanza(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := f.provider().Quote(ctx, "5247")
		if !errors.Is(err, domain.ErrMarketDataUnavailable) || !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want ErrMarketDataUnavailable wrapping context.Canceled", err)
		}
	})
}

func TestParseSwedishNumber(t *testing.T) {
	cases := []struct {
		in    string
		want  float64
		valid bool
	}{
		{"1,2", 1.2, true},
		{"−24,23", -24.23, true},
		{"-0,5", -0.5, true},
		{"+3,10", 3.1, true},
		{"1 234,50", 1234.5, true},
		{"1 234,50", 1234.5, true},
		{"2,5 %", 2.5, true},
		{"", 0, false},
		{"-", 0, false},
	}
	for _, c := range cases {
		got, ok, err := parseSwedishNumber(c.in)
		if err != nil || ok != c.valid || got != c.want {
			t.Errorf("parseSwedishNumber(%q) = %v, %v, %v; want %v, %v", c.in, got, ok, err, c.want, c.valid)
		}
	}
	if _, _, err := parseSwedishNumber("abc"); err == nil {
		t.Error(`parseSwedishNumber("abc") should fail`)
	}
}

func assertFloatPtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %v", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}

func assertAscending(t *testing.T, candles []domain.Candle) {
	t.Helper()
	for i := 1; i < len(candles); i++ {
		if !candles[i].T.After(candles[i-1].T) {
			t.Errorf("candles not strictly ascending at %d: %v then %v", i, candles[i-1].T, candles[i].T)
		}
	}
}
