package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"backend/internal/domain"
	"backend/internal/testutil"
	"github.com/shopspring/decimal"
)

// Story NW-08: LIMIT/STOP placement, GET and DELETE /orders/{id}, and the status filter.

// kallMarket knows SIM-KALL (details only: pending orders need no quote).
func kallMarket() *testutil.MarketProvider {
	return &testutil.MarketProvider{DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
		if id != "SIM-KALL" {
			return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
		}
		return domain.InstrumentDetails{OrderbookID: id, Symbol: "KALL", Name: "Kallsten Bank", LastPrice: 100}, nil
	}}
}

const buyLimit = `{"orderbook_id":"SIM-KALL","side":"BUY","type":"LIMIT","quantity":10,"limit_price":95}`

var orderKeys = []string{"commission", "created_at", "fill_price", "filled_at", "id", "limit_price", "name", "note", "orderbook_id",
	"quantity", "reject_reason", "side", "status", "stop_price", "symbol", "total", "type"}

func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return m
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestOrderHandler_PlaceOrder_LimitIsOpenWithNullFill(t *testing.T) {
	repo := &testutil.OrderRepo{}
	w := postOrder(t, orderEngine(repo, kallMarket()), memberSession, "", buyLimit)
	expectStatus(t, w, http.StatusCreated)
	body := decodeMap(t, w.Body.Bytes())
	if got := keysOf(body); !reflect.DeepEqual(got, orderKeys) {
		t.Errorf("order keys = %v, want %v", got, orderKeys)
	}
	want := map[string]any{"status": "OPEN", "type": "LIMIT", "side": "BUY", "quantity": float64(10), "limit_price": float64(95),
		"stop_price": nil, "fill_price": nil, "commission": nil, "total": nil, "reject_reason": nil, "filled_at": nil}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v (body %s)", k, body[k], v, w.Body.String())
		}
	}
	if !strings.Contains(w.Body.String(), `"limit_price":95.00`) {
		t.Errorf("limit_price must be serialized with 2 decimals: %s", w.Body.String())
	}
	if len(repo.Pending) != 1 || !repo.Pending[0].ReservedCash.Equal(decimalOf("952.38")) {
		t.Errorf("PlacePending params = %+v, want a 952.38 reservation", repo.Pending)
	}
}

func TestOrderHandler_PlaceOrder_MarketOrderKeepsFillAndNullPrices(t *testing.T) {
	w := postOrder(t, orderEngine(&testutil.OrderRepo{}, fjrdMarket()), memberSession, "", buy10)
	expectStatus(t, w, http.StatusCreated)
	body := decodeMap(t, w.Body.Bytes())
	if body["fill_price"] != 100.10 || body["limit_price"] != nil || body["stop_price"] != nil || body["reject_reason"] != nil {
		t.Errorf("market order body = %s", w.Body.String())
	}
}

func TestOrderHandler_PlaceOrder_PendingErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		repoErr  error
		status   int
		wantCode string
	}{
		{"AC3 missing limit_price", `{"orderbook_id":"SIM-KALL","side":"BUY","type":"LIMIT","quantity":10}`, nil, 400, domain.CodeValidation},
		{"AC3 limit_price 0", `{"orderbook_id":"SIM-KALL","side":"BUY","type":"LIMIT","quantity":10,"limit_price":0}`, nil, 400, domain.CodeValidation},
		{"AC3 limit_price -1", `{"orderbook_id":"SIM-KALL","side":"BUY","type":"LIMIT","quantity":10,"limit_price":-1}`, nil, 400, domain.CodeValidation},
		{"limit_price null counts as missing", `{"orderbook_id":"SIM-KALL","side":"BUY","type":"LIMIT","quantity":10,"limit_price":null}`, nil, 400, domain.CodeValidation},
		{"limit_price as a string", `{"orderbook_id":"SIM-KALL","side":"BUY","type":"LIMIT","quantity":10,"limit_price":"95"}`, nil, 400, domain.CodeValidation},
		{"stop_price as a string", `{"orderbook_id":"SIM-KALL","side":"SELL","type":"STOP","quantity":10,"stop_price":"90"}`, nil, 400, domain.CodeValidation},
		{"stop_price as an object", `{"orderbook_id":"SIM-KALL","side":"SELL","type":"STOP","quantity":10,"stop_price":{}}`, nil, 400, domain.CodeValidation},
		{"AC3 reservation over buying power", buyLimit, domain.ErrInsufficientFunds, 422, domain.CodeInsufficientFunds},
		{"AC3 sell over available shares", `{"orderbook_id":"SIM-KALL","side":"SELL","type":"LIMIT","quantity":11,"limit_price":105}`, domain.ErrInsufficientShares, 422, domain.CodeInsufficientShares},
		{"unknown instrument", `{"orderbook_id":"SIM-NOPE","side":"BUY","type":"LIMIT","quantity":1,"limit_price":1}`, nil, 404, domain.CodeInstrumentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			if tt.repoErr != nil {
				repo.PlacePendingFn = func(context.Context, domain.PendingOrderParams) (*domain.Order, bool, error) {
					return nil, false, tt.repoErr
				}
			}
			w := postOrder(t, orderEngine(repo, kallMarket()), memberSession, "", tt.body)
			expectStatus(t, w, tt.status)
			expectErrorCode(t, w, tt.wantCode)
		})
	}
}

func TestOrderHandler_GetOrder(t *testing.T) {
	repo := &testutil.OrderRepo{}
	r := orderEngine(repo, kallMarket())
	expectStatus(t, postOrder(t, r, memberSession, "", buyLimit), http.StatusCreated)

	w := do(t, r, http.MethodGet, "/api/v1/orders/1", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	if body := decodeMap(t, w.Body.Bytes()); body["id"] != float64(1) || body["status"] != "OPEN" {
		t.Errorf("GET /orders/1 = %s", w.Body.String())
	}

	for _, tt := range []struct {
		name, path, session string
		status              int
		code                string
	}{
		{"another user's order", "/api/v1/orders/1", adminSession, 404, domain.CodeOrderNotFound},
		{"unknown id", "/api/v1/orders/42", memberSession, 404, domain.CodeOrderNotFound},
		{"id 0", "/api/v1/orders/0", memberSession, 400, domain.CodeValidation},
		{"negative id", "/api/v1/orders/-1", memberSession, 400, domain.CodeValidation},
		{"non-numeric id", "/api/v1/orders/abc", memberSession, 400, domain.CodeValidation},
		{"no session", "/api/v1/orders/1", "", 401, domain.CodeUnauthenticated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := do(t, r, http.MethodGet, tt.path, tt.session, nil)
			expectStatus(t, w, tt.status)
			expectErrorCode(t, w, tt.code)
		})
	}
}

func TestOrderHandler_CancelOrder(t *testing.T) {
	repo := &testutil.OrderRepo{}
	r := orderEngine(repo, kallMarket())
	expectStatus(t, postOrder(t, r, memberSession, "", buyLimit), http.StatusCreated)

	// AC4: another guest's id is 404 ORDER_NOT_FOUND and changes nothing.
	w := do(t, r, http.MethodDelete, "/api/v1/orders/1", adminSession, nil)
	expectStatus(t, w, http.StatusNotFound)
	expectErrorCode(t, w, domain.CodeOrderNotFound)

	// AC4: 200 with status CANCELLED.
	w = do(t, r, http.MethodDelete, "/api/v1/orders/1", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	body := decodeMap(t, w.Body.Bytes())
	if body["status"] != "CANCELLED" || body["id"] != float64(1) || body["fill_price"] != nil {
		t.Errorf("DELETE /orders/1 = %s", w.Body.String())
	}

	// AC4: again is 409 ORDER_NOT_OPEN.
	w = do(t, r, http.MethodDelete, "/api/v1/orders/1", memberSession, nil)
	expectStatus(t, w, http.StatusConflict)
	expectErrorCode(t, w, domain.CodeOrderNotOpen)

	w = do(t, r, http.MethodDelete, "/api/v1/orders/x", memberSession, nil)
	expectStatus(t, w, http.StatusBadRequest)
	expectErrorCode(t, w, domain.CodeValidation)

	w = do(t, r, http.MethodDelete, "/api/v1/orders/1", "", nil)
	expectStatus(t, w, http.StatusUnauthorized)
}

func TestOrderHandler_ListOrders_StatusFilter(t *testing.T) {
	tests := []struct {
		name, query string
		want        []domain.OrderStatus
		status      int
	}{
		{"AC6 open only", "?status=OPEN", []domain.OrderStatus{domain.OrderStatusOpen}, 200},
		{"history", "?status=FILLED,CANCELLED,REJECTED", []domain.OrderStatus{domain.OrderStatusFilled, domain.OrderStatusCancelled, domain.OrderStatusRejected}, 200},
		{"spaces around commas", "?status=FILLED,%20REJECTED", []domain.OrderStatus{domain.OrderStatusFilled, domain.OrderStatusRejected}, 200},
		{"no filter", "", nil, 200},
		{"unknown status", "?status=PENDING", nil, 400},
		{"empty status", "?status=", nil, 400},
		{"lowercase", "?status=open", nil, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []domain.OrderStatus
			repo := &testutil.OrderRepo{ListByUserFn: func(_ context.Context, _ uint, _ *domain.OrderCursor, _ int, statuses ...domain.OrderStatus) ([]domain.Order, error) {
				got = statuses
				return []domain.Order{}, nil
			}}
			w := do(t, orderEngine(repo, kallMarket()), http.MethodGet, "/api/v1/orders"+tt.query, memberSession, nil)
			expectStatus(t, w, tt.status)
			if tt.status != 200 {
				expectErrorCode(t, w, domain.CodeValidation)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("statuses = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrderHandler_ListOrders_OpenOnlyEndToEndOnTheFake(t *testing.T) {
	repo := &testutil.OrderRepo{}
	r := orderEngine(repo, kallMarket())
	expectStatus(t, postOrder(t, r, memberSession, "", buyLimit), http.StatusCreated)
	expectStatus(t, postOrder(t, r, memberSession, "", buyLimit), http.StatusCreated)
	expectStatus(t, do(t, r, http.MethodDelete, "/api/v1/orders/1", memberSession, nil), http.StatusOK)

	w := do(t, r, http.MethodGet, "/api/v1/orders?status=OPEN", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0]["id"] != float64(2) || page.Items[0]["status"] != "OPEN" {
		t.Errorf("GET /orders?status=OPEN = %s, want only order 2", w.Body.String())
	}
}

func decimalOf(s string) decimal.Decimal { return decimal.RequireFromString(s) }
