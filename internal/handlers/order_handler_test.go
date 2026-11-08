package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// ---------------------------------------------------------------------------
// GET /orders
// ---------------------------------------------------------------------------

// getOrders sends GET /api/v1/orders with the given raw query and optional session.
func getOrders(t *testing.T, r http.Handler, session, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/orders"
	if query != "" {
		path += "?" + query
	}
	return do(t, r, http.MethodGet, path, session, nil)
}

// orderPageBody is the decoded OrderPage; NextCursor stays a raw message so null is distinguishable.
type orderPageBody struct {
	Items      []map[string]any `json:"items"`
	NextCursor json.RawMessage  `json:"next_cursor"`
}

func decodeOrderPage(t *testing.T, w *httptest.ResponseRecorder) (orderPageBody, *string) {
	t.Helper()
	var page orderPageBody
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode OrderPage: %v (body %s)", err, w.Body.String())
	}
	var cursor *string
	if err := json.Unmarshal(page.NextCursor, &cursor); err != nil {
		t.Fatalf("next_cursor must be a string or null: %s", w.Body.String())
	}
	return page, cursor
}

func TestOrderHandler_ListOrders_EmptyShape(t *testing.T) {
	w := getOrders(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), memberSession, "")
	expectStatus(t, w, http.StatusOK)
	if got := w.Body.String(); got != `{"items":[],"next_cursor":null}` {
		t.Errorf("body = %s, want {\"items\":[],\"next_cursor\":null}", got)
	}
}

func TestOrderHandler_ListOrders_PagesWithLimitOne(t *testing.T) {
	repo := &testutil.OrderRepo{}
	r := orderEngine(repo, fjrdMarket())
	expectStatus(t, postOrder(t, r, memberSession, "", buy10), http.StatusCreated)
	expectStatus(t, postOrder(t, r, memberSession, "", `{"orderbook_id":"SIM-FJRD","side":"SELL","quantity":4}`), http.StatusCreated)

	first := getOrders(t, r, memberSession, "limit=1")
	expectStatus(t, first, http.StatusOK)
	page, cursor := decodeOrderPage(t, first)
	if len(page.Items) != 1 || page.Items[0]["id"] != float64(2) || page.Items[0]["side"] != "SELL" {
		t.Fatalf("first page items = %v, want the newest order (id 2, SELL)", page.Items)
	}
	if cursor == nil || *cursor == "" {
		t.Fatalf("first page next_cursor = null, want a cursor: %s", first.Body.String())
	}
	for _, key := range []string{"orderbook_id", "symbol", "name", "type", "status", "quantity", "fill_price", "commission", "total", "created_at", "filled_at"} {
		if _, ok := page.Items[0][key]; !ok {
			t.Errorf("order lacks %q: %s", key, first.Body.String())
		}
	}

	second := getOrders(t, r, memberSession, "limit=1&cursor="+url.QueryEscape(*cursor))
	expectStatus(t, second, http.StatusOK)
	page, cursor = decodeOrderPage(t, second)
	if len(page.Items) != 1 || page.Items[0]["id"] != float64(1) || page.Items[0]["side"] != "BUY" {
		t.Fatalf("second page items = %v, want the older order (id 1, BUY)", page.Items)
	}
	if cursor != nil {
		t.Errorf("second page next_cursor = %q, want null", *cursor)
	}

	all := getOrders(t, r, memberSession, "")
	expectStatus(t, all, http.StatusOK)
	if page, cursor := decodeOrderPage(t, all); len(page.Items) != 2 || cursor != nil {
		t.Errorf("default page = %d items, cursor %v; want 2 items, null", len(page.Items), cursor)
	}

	other := getOrders(t, r, adminSession, "")
	expectStatus(t, other, http.StatusOK)
	if got := other.Body.String(); got != `{"items":[],"next_cursor":null}` {
		t.Errorf("another user's page = %s, want empty", got)
	}
}

func TestOrderHandler_ListOrders_DefaultLimit(t *testing.T) {
	var gotLimit int
	repo := &testutil.OrderRepo{ListByUserFn: func(_ context.Context, userID uint, _ *domain.OrderCursor, limit int, _ ...domain.OrderStatus) ([]domain.Order, error) {
		gotLimit = limit
		if userID != 1 {
			t.Errorf("ListByUser user = %d, want 1", userID)
		}
		return []domain.Order{}, nil
	}}
	expectStatus(t, getOrders(t, orderEngine(repo, fjrdMarket()), memberSession, ""), http.StatusOK)
	if gotLimit != domain.DefaultOrderPageLimit+1 {
		t.Errorf("repository limit = %d, want %d", gotLimit, domain.DefaultOrderPageLimit+1)
	}
}

func TestOrderHandler_ListOrders_BadRequests(t *testing.T) {
	queries := map[string]string{
		"limit=0":       "limit=0",
		"limit=abc":     "limit=abc",
		"limit=101":     "limit=101",
		"limit=-5":      "limit=-5",
		"limit=1.5":     "limit=1.5",
		"empty limit":   "limit=",
		"bad cursor":    "cursor=not-a-cursor",
		"tampered json": "cursor=" + url.QueryEscape("eyJjIjoibm93IiwiaSI6NDN9"),
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			called := false
			repo := &testutil.OrderRepo{ListByUserFn: func(context.Context, uint, *domain.OrderCursor, int, ...domain.OrderStatus) ([]domain.Order, error) {
				called = true
				return []domain.Order{}, nil
			}}
			w := getOrders(t, orderEngine(repo, fjrdMarket()), memberSession, query)
			expectStatus(t, w, http.StatusBadRequest)
			expectErrorCode(t, w, domain.CodeValidation)
			if called {
				t.Error("the repository must not be called for an invalid request")
			}
		})
	}

	t.Run("limit message", func(t *testing.T) {
		w := getOrders(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), memberSession, "limit=abc")
		if !strings.Contains(w.Body.String(), `"error":"limit must be an integer from 1 to 100"`) {
			t.Errorf("body = %s", w.Body.String())
		}
	})
}

func TestOrderHandler_ListOrders_RepositoryFailure(t *testing.T) {
	repo := &testutil.OrderRepo{ListByUserFn: func(context.Context, uint, *domain.OrderCursor, int, ...domain.OrderStatus) ([]domain.Order, error) {
		return nil, errors.New("db down")
	}}
	w := getOrders(t, orderEngine(repo, fjrdMarket()), memberSession, "")
	expectStatus(t, w, http.StatusInternalServerError)
	expectErrorCode(t, w, domain.CodeInternal)
}

func TestOrderHandler_ListOrders_Unauthenticated(t *testing.T) {
	for _, session := range []string{"", "unknown-session"} {
		w := getOrders(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), session, "")
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	}
}

func patchOrderNote(t *testing.T, r http.Handler, session, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/orders/"+id+"/note", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "session_id", Value: session})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestOrderHandler_UpdateOrderNote(t *testing.T) {
	repo := &testutil.OrderRepo{}
	engine := orderEngine(repo, fjrdMarket())

	// Create an order first
	wPost := postOrder(t, engine, memberSession, "", `{"orderbook_id":"SIM-FJRD","side":"BUY","quantity":10,"note":"initial"}`)
	expectStatus(t, wPost, http.StatusCreated)

	// Successful update
	wPatch := patchOrderNote(t, engine, memberSession, "1", `{"note":"updated thesis"}`)
	expectStatus(t, wPatch, http.StatusOK)
	var updated domain.Order
	if err := json.Unmarshal(wPatch.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Note != "updated thesis" {
		t.Fatalf("updated.Note = %q, want %q", updated.Note, "updated thesis")
	}

	// >500 characters
	longNote := strings.Repeat("x", 501)
	wLong := patchOrderNote(t, engine, memberSession, "1", `{"note":"`+longNote+`"}`)
	expectStatus(t, wLong, http.StatusBadRequest)
	expectErrorCode(t, wLong, domain.CodeValidation)

	// Non-existent order
	wNotFound := patchOrderNote(t, engine, memberSession, "999", `{"note":"valid"}`)
	expectStatus(t, wNotFound, http.StatusNotFound)
	expectErrorCode(t, wNotFound, domain.CodeOrderNotFound)

	// Another user's order returns 404
	wOtherUser := patchOrderNote(t, engine, adminSession, "1", `{"note":"valid"}`)
	expectStatus(t, wOtherUser, http.StatusNotFound)
	expectErrorCode(t, wOtherUser, domain.CodeOrderNotFound)

	// Invalid id
	wBadID := patchOrderNote(t, engine, memberSession, "abc", `{"note":"valid"}`)
	expectStatus(t, wBadID, http.StatusBadRequest)
	expectErrorCode(t, wBadID, domain.CodeValidation)

	// Unauthenticated
	wUnauth := patchOrderNote(t, engine, "", "1", `{"note":"valid"}`)
	expectStatus(t, wUnauth, http.StatusUnauthorized)
	expectErrorCode(t, wUnauth, domain.CodeUnauthenticated)
}
