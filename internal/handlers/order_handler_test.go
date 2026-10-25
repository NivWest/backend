package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
)

// fjrdMarket is SIM-FJRD pinned at 100 (bid 99.90, ask 100.10); every other id is unknown.
func fjrdMarket() *testutil.MarketProvider {
	return &testutil.MarketProvider{
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			if id != "SIM-FJRD" {
				return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
			}
			return domain.InstrumentDetails{OrderbookID: id, Symbol: "FJRD", Name: "Fjord Robotics", LastPrice: 100}, nil
		},
		QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
			if id != "SIM-FJRD" {
				return domain.Quote{}, domain.ErrInstrumentNotFound
			}
			return domain.Quote{OrderbookID: id, Symbol: "FJRD", Last: 100, Bid: 99.90, Ask: 100.10}, nil
		},
	}
}

// newOrderService builds an OrderService without the simulated latency.
func newOrderService(repo *testutil.OrderRepo, provider *testutil.MarketProvider) *services.OrderService {
	svc := services.NewOrderService(repo, provider)
	svc.SetSleep(func(time.Duration) {})
	return svc
}

// orderEngine mounts an OrderHandler over repo and provider.
func orderEngine(repo *testutil.OrderRepo, provider *testutil.MarketProvider) *gin.Engine {
	return newEngine(NewOrderHandler(newOrderService(repo, provider), newAuth()))
}

// postOrder sends POST /api/v1/orders with a raw JSON body, an optional session and an optional Idempotency-Key.
func postOrder(t *testing.T, r http.Handler, session, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "session_id", Value: session})
	}
	if key != "" {
		req.Header.Set(domain.IdempotencyKeyHeader, key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const buy10 = `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":10}`

func TestOrderHandler_PlaceOrder_Created(t *testing.T) {
	repo := &testutil.OrderRepo{}
	w := postOrder(t, orderEngine(repo, fjrdMarket()), memberSession, "", buy10)
	expectStatus(t, w, http.StatusCreated)

	body := w.Body.String()
	for _, want := range []string{
		`"id":1`, `"orderbook_id":"SIM-FJRD"`, `"symbol":"FJRD"`, `"name":"Fjord Robotics"`,
		`"side":"BUY"`, `"type":"MARKET"`, `"status":"FILLED"`, `"quantity":10`,
		`"fill_price":100.10`, `"commission":2.50`, `"total":1003.50`, `"created_at":"`, `"filled_at":"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if len(repo.Executed) != 1 || repo.Executed[0].UserID != 1 {
		t.Errorf("Execute calls = %+v, want one for user 1", repo.Executed)
	}
}

func TestOrderHandler_PlaceOrder_SellMinimumCommission(t *testing.T) {
	w := postOrder(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), memberSession, "",
		`{"orderbook_id":"SIM-FJRD","side":"SELL","quantity":4,"type":"MARKET"}`)
	expectStatus(t, w, http.StatusCreated)
	for _, want := range []string{`"fill_price":99.90`, `"commission":1.00`, `"total":398.60`, `"quantity":4`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body lacks %s: %s", want, w.Body.String())
		}
	}
}

func TestOrderHandler_PlaceOrder_IdempotentReplay(t *testing.T) {
	repo := &testutil.OrderRepo{}
	r := orderEngine(repo, fjrdMarket())

	first := postOrder(t, r, memberSession, "k-123", buy10)
	expectStatus(t, first, http.StatusCreated)
	second := postOrder(t, r, memberSession, "k-123", buy10)
	expectStatus(t, second, http.StatusOK)

	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Errorf("replay body differs:\n first  %s\n second %s", first.Body, second.Body)
	}
	if len(repo.Executed) != 1 {
		t.Errorf("Execute called %d times, want 1", len(repo.Executed))
	}

	conflict := postOrder(t, r, memberSession, "k-123", `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":11}`)
	expectStatus(t, conflict, http.StatusConflict)
	expectErrorCode(t, conflict, domain.CodeConflict)
}

func TestOrderHandler_PlaceOrder_BadRequests(t *testing.T) {
	bodies := map[string]string{
		"malformed json":              `{`,
		"not an object":               `[1]`,
		"quantity as string":          `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":"10"}`,
		"quantity as bool":            `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":true}`,
		"side as number":              `{"orderbook_id":"SIM-FJRD","side":1,"quantity":10}`,
		"missing orderbook_id":        `{"side":"BUY","quantity":10}`,
		"blank orderbook_id":          `{"orderbook_id":"  ","side":"BUY","quantity":10}`,
		"side HOLD":                   `{"orderbook_id":"SIM-FJRD","side":"HOLD","quantity":10}`,
		"quantity 0":                  `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":0}`,
		"quantity -1":                 `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":-1}`,
		"quantity with 5 decimals":    `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":1.00001}`,
		"missing quantity":            `{"orderbook_id":"SIM-FJRD","side":"BUY"}`,
		"type LIMIT":                  `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":1,"type":"LIMIT"}`,
		"legacy {symbol, type: BUY}":  `{"symbol":"FJRD","type":"BUY","quantity":1}`,
		"legacy with an orderbook id": `{"orderbook_id":"SIM-FJRD","type":"BUY","quantity":1}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			w := postOrder(t, orderEngine(repo, fjrdMarket()), memberSession, "", body)
			expectStatus(t, w, http.StatusBadRequest)
			expectErrorCode(t, w, domain.CodeValidation)
			if len(repo.Executed) != 0 {
				t.Error("no order may be executed for an invalid request")
			}
		})
	}

	t.Run("invalid idempotency key", func(t *testing.T) {
		w := postOrder(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), memberSession, strings.Repeat("k", 256), buy10)
		expectStatus(t, w, http.StatusBadRequest)
		expectErrorCode(t, w, domain.CodeValidation)
	})

	t.Run("message comes from the validation error", func(t *testing.T) {
		w := postOrder(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), memberSession, "",
			`{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":0}`)
		if want := `{"error":"quantity must be greater than 0","code":"VALIDATION_ERROR"}`; w.Body.String() != want {
			t.Errorf("body = %s, want %s", w.Body, want)
		}
	})
}

func TestOrderHandler_PlaceOrder_ErrorMapping(t *testing.T) {
	failingExecute := func(err error) *testutil.OrderRepo {
		return &testutil.OrderRepo{ExecuteFn: func(context.Context, domain.ExecuteOrderParams) (*domain.Order, bool, error) {
			return nil, false, err
		}}
	}
	quoteFails := func(err error) *testutil.MarketProvider {
		p := fjrdMarket()
		p.QuoteFn = func(context.Context, string) (domain.Quote, error) { return domain.Quote{}, err }
		return p
	}

	tests := []struct {
		name       string
		repo       *testutil.OrderRepo
		provider   *testutil.MarketProvider
		body       string
		wantStatus int
		wantBody   string
	}{
		{
			name: "unknown instrument", repo: &testutil.OrderRepo{}, provider: fjrdMarket(),
			body:       `{"orderbook_id":"SIM-NOPE","side":"BUY","quantity":1}`,
			wantStatus: http.StatusNotFound, wantBody: `{"error":"instrument not found","code":"INSTRUMENT_NOT_FOUND"}`,
		},
		{
			name: "insufficient funds", repo: failingExecute(domain.ErrInsufficientFunds), provider: fjrdMarket(), body: buy10,
			wantStatus: http.StatusUnprocessableEntity, wantBody: `{"error":"not enough cash for this order","code":"INSUFFICIENT_FUNDS"}`,
		},
		{
			name: "insufficient shares", repo: failingExecute(fmt.Errorf("sell: %w", domain.ErrInsufficientShares)), provider: fjrdMarket(), body: buy10,
			wantStatus: http.StatusUnprocessableEntity, wantBody: `{"error":"not enough shares to sell","code":"INSUFFICIENT_SHARES"}`,
		},
		{
			name: "market data unavailable", repo: &testutil.OrderRepo{}, provider: quoteFails(errors.New("timeout")), body: buy10,
			wantStatus: http.StatusBadGateway, wantBody: `{"error":"market data unavailable","code":"MARKET_DATA_UNAVAILABLE"}`,
		},
		{
			name: "unexpected repository error", repo: failingExecute(errors.New("db down: secret detail")), provider: fjrdMarket(), body: buy10,
			wantStatus: http.StatusInternalServerError, wantBody: `{"error":"internal server error","code":"INTERNAL_ERROR"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := postOrder(t, orderEngine(tt.repo, tt.provider), memberSession, "", tt.body)
			expectStatus(t, w, tt.wantStatus)
			if w.Body.String() != tt.wantBody {
				t.Errorf("body = %s, want %s", w.Body, tt.wantBody)
			}
		})
	}
}

func TestOrderHandler_PlaceOrder_Unauthenticated(t *testing.T) {
	t.Run("no session", func(t *testing.T) {
		repo := &testutil.OrderRepo{}
		w := postOrder(t, orderEngine(repo, fjrdMarket()), "", "", buy10)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
		if len(repo.Executed) != 0 {
			t.Error("no order may be executed without a session")
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		w := postOrder(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), "nope", "", buy10)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	})

	t.Run("handler without a user in the context", func(t *testing.T) {
		h := NewOrderHandler(newOrderService(&testutil.OrderRepo{}, fjrdMarket()), newAuth())
		r := gin.New()
		r.POST("/orders", h.PlaceOrder)
		req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(buy10))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	})
}
