package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
	"backend/internal/testutil"
)

// Story NW-08: LIMIT and STOP placement, lookup and cancel in OrderService.

// kallProvider knows SIM-KALL only. Quote must not be needed by pending orders, so it fails.
func kallProvider(quoted *int) *testutil.MarketProvider {
	return &testutil.MarketProvider{
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			if id != "SIM-KALL" {
				return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
			}
			return domain.InstrumentDetails{OrderbookID: id, Symbol: "KALL", Name: "Kallsten Bank", LastPrice: 100}, nil
		},
		QuoteFn: func(context.Context, string) (domain.Quote, error) {
			if quoted != nil {
				*quoted++
			}
			return domain.Quote{}, errors.New("pending orders need no quote")
		},
	}
}

// price is a pointer to the decimal s, for limit and stop prices.
func price(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

func pending(side domain.OrderSide, t domain.OrderType, qty string, limit, stop string) domain.OrderRequest {
	req := domain.OrderRequest{OrderbookID: "SIM-KALL", Side: side, Type: t, Quantity: dec(qty)}
	if limit != "" {
		req.LimitPrice = price(limit)
	}
	if stop != "" {
		req.StopPrice = price(stop)
	}
	return req
}

func TestOrderService_PlaceOrder_PendingValidation(t *testing.T) {
	buy, sell := domain.OrderSideBuy, domain.OrderSideSell
	lim, stp, mkt := domain.OrderTypeLimit, domain.OrderTypeStop, domain.OrderTypeMarket
	tests := []struct {
		name    string
		req     domain.OrderRequest
		wantMsg string
	}{
		{"AC3 limit without limit_price", pending(buy, lim, "10", "", ""), "limit_price is required for a LIMIT order"},
		{"AC3 limit_price 0", pending(buy, lim, "10", "0", ""), "limit_price must be greater than 0"},
		{"AC3 limit_price negative", pending(sell, lim, "10", "-5", ""), "limit_price must be greater than 0"},
		{"limit_price with 3 decimals", pending(buy, lim, "10", "95.001", ""), "limit_price must have at most 2 decimals"},
		{"stop without stop_price", pending(sell, stp, "10", "", ""), "stop_price is required for a STOP order"},
		{"stop_price 0", pending(sell, stp, "10", "", "0"), "stop_price must be greater than 0"},
		{"stop_price with 3 decimals", pending(sell, stp, "10", "", "89.999"), "stop_price must have at most 2 decimals"},
		{"stop_price on a limit order", pending(buy, lim, "10", "95", "90"), "stop_price is not allowed on a LIMIT order"},
		{"limit_price on a stop order", pending(sell, stp, "10", "95", "90"), "limit_price is not allowed on a STOP order"},
		{"limit_price on a market order", pending(buy, mkt, "10", "95", ""), "limit_price and stop_price are not allowed on a MARKET order"},
		{"stop_price on a default-type order", pending(buy, "", "10", "", "90"), "limit_price and stop_price are not allowed on a MARKET order"},
		{"quantity still validated", pending(buy, lim, "0", "95", ""), "quantity must be greater than 0"},
		{"lowercase type", pending(buy, "limit", "10", "95", ""), "type must be MARKET, LIMIT or STOP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			_, _, err := newTestOrderService(repo, kallProvider(nil)).PlaceOrder(context.Background(), 1, "", tt.req)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) || verr.Msg != tt.wantMsg {
				t.Fatalf("error = %v, want ValidationError %q", err, tt.wantMsg)
			}
			if len(repo.Pending) != 0 || len(repo.Executed) != 0 {
				t.Errorf("nothing may be stored for an invalid order")
			}
		})
	}
}

func TestOrderService_PlaceOrder_PendingStoresOpenWithReservation(t *testing.T) {
	tests := []struct {
		name         string
		req          domain.OrderRequest
		wantReserved string
	}{
		{"AC1 buy limit 10 @ 95", pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "95", ""), "952.38"},
		{"buy stop 10 @ 90", pending(domain.OrderSideBuy, domain.OrderTypeStop, "10", "", "90"), "947.36"},
		{"AC5 sell stop 10 @ 90 reserves shares", pending(domain.OrderSideSell, domain.OrderTypeStop, "10", "", "90"), "0"},
		{"sell limit", pending(domain.OrderSideSell, domain.OrderTypeLimit, "3", "105", ""), "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			quoted, slept := 0, 0
			svc := NewOrderService(repo, kallProvider(&quoted))
			svc.sleep = func(time.Duration) { slept++ }

			order, created, err := svc.PlaceOrder(context.Background(), 1, "k-1", tt.req)
			if err != nil || !created {
				t.Fatalf("PlaceOrder() = %+v, %v, %v", order, created, err)
			}
			if order.Status != domain.OrderStatusOpen || order.Type != tt.req.Type {
				t.Errorf("order = %s %s, want OPEN %s", order.Status, order.Type, tt.req.Type)
			}
			if quoted != 0 || slept != 0 || len(repo.Executed) != 0 {
				t.Errorf("a pending order must not quote (%d), sleep (%d) or execute (%d)", quoted, slept, len(repo.Executed))
			}
			if len(repo.Pending) != 1 {
				t.Fatalf("PlacePending calls = %d, want 1", len(repo.Pending))
			}
			p := repo.Pending[0]
			if p.UserID != 1 || p.StockID != 1 || p.IdempotencyKey != "k-1" || p.Side != tt.req.Side || !p.Quantity.Equal(tt.req.Quantity) {
				t.Errorf("params = %+v", p)
			}
			if !p.ReservedCash.Equal(dec(tt.wantReserved)) {
				t.Errorf("ReservedCash = %s, want %s", p.ReservedCash, tt.wantReserved)
			}
			if !samePrice(p.LimitPrice, tt.req.LimitPrice) || !samePrice(p.StopPrice, tt.req.StopPrice) {
				t.Errorf("prices = %v/%v, want %v/%v", p.LimitPrice, p.StopPrice, tt.req.LimitPrice, tt.req.StopPrice)
			}
		})
	}
}

func TestOrderService_PlaceOrder_PendingErrors(t *testing.T) {
	req := pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "95", "")
	tests := []struct {
		name    string
		req     domain.OrderRequest
		repoErr error
		want    error
	}{
		{"AC3 reservation over buying power", req, domain.ErrInsufficientFunds, domain.ErrInsufficientFunds},
		{"AC3 sell over available shares", pending(domain.OrderSideSell, domain.OrderTypeLimit, "50", "105", ""), domain.ErrInsufficientShares, domain.ErrInsufficientShares},
		{"unknown instrument", domain.OrderRequest{OrderbookID: "SIM-NOPE", Side: domain.OrderSideBuy, Type: domain.OrderTypeLimit, Quantity: dec("1"), LimitPrice: price("1")}, nil, domain.ErrInstrumentNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{PlacePendingFn: func(context.Context, domain.PendingOrderParams) (*domain.Order, bool, error) {
				return nil, false, tt.repoErr
			}}
			_, _, err := newTestOrderService(repo, kallProvider(nil)).PlaceOrder(context.Background(), 1, "", tt.req)
			if !errors.Is(err, tt.want) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOrderService_PlaceOrder_PendingIdempotency(t *testing.T) {
	repo := &testutil.OrderRepo{}
	svc := newTestOrderService(repo, kallProvider(nil))
	req := pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "95", "")
	first, created, err := svc.PlaceOrder(context.Background(), 1, "k-1", req)
	if err != nil || !created {
		t.Fatalf("first PlaceOrder() = %v, %v", created, err)
	}

	again, created, err := svc.PlaceOrder(context.Background(), 1, "k-1", pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "95.00", ""))
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("replay = %+v, %v, %v; want the first order, created=false", again, created, err)
	}
	if len(repo.Pending) != 1 {
		t.Errorf("a replay must not place again (PlacePending calls %d)", len(repo.Pending))
	}

	for name, other := range map[string]domain.OrderRequest{
		"other limit_price": pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "96", ""),
		"other type":        pending(domain.OrderSideBuy, domain.OrderTypeStop, "10", "", "95"),
		"market":            {OrderbookID: "SIM-KALL", Side: domain.OrderSideBuy, Quantity: dec("10")},
	} {
		if _, _, err := svc.PlaceOrder(context.Background(), 1, "k-1", other); !errors.Is(err, domain.ErrIdempotencyKeyReused) {
			t.Errorf("%s: error = %v, want ErrIdempotencyKeyReused", name, err)
		}
	}
}

func TestOrderService_GetAndCancelOrder(t *testing.T) {
	repo := &testutil.OrderRepo{}
	svc := newTestOrderService(repo, kallProvider(nil))
	ctx := context.Background()
	placed, _, err := svc.PlaceOrder(ctx, 1, "", pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "95", ""))
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetOrder(ctx, 1, placed.ID)
	if err != nil || got.ID != placed.ID || got.Status != domain.OrderStatusOpen {
		t.Fatalf("GetOrder() = %+v, %v", got, err)
	}
	if _, err := svc.GetOrder(ctx, 2, placed.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("GetOrder(other user) error = %v, want ErrOrderNotFound", err)
	}
	if _, err := svc.CancelOrder(ctx, 2, placed.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("CancelOrder(other user) error = %v, want ErrOrderNotFound (AC4)", err)
	}

	cancelled, err := svc.CancelOrder(ctx, 1, placed.ID)
	if err != nil || cancelled.Status != domain.OrderStatusCancelled {
		t.Fatalf("CancelOrder() = %+v, %v; want CANCELLED (AC4)", cancelled, err)
	}
	if _, err := svc.CancelOrder(ctx, 1, placed.ID); !errors.Is(err, domain.ErrOrderNotOpen) {
		t.Errorf("CancelOrder(again) error = %v, want ErrOrderNotOpen (AC4)", err)
	}
	if _, err := svc.CancelOrder(ctx, 1, 999); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("CancelOrder(unknown) error = %v, want ErrOrderNotFound", err)
	}
}

func TestOrderService_ListOrders_StatusFilter(t *testing.T) {
	repo := &testutil.OrderRepo{}
	svc := newTestOrderService(repo, kallProvider(nil))
	ctx := context.Background()
	open, _, _ := svc.PlaceOrder(ctx, 1, "", pending(domain.OrderSideBuy, domain.OrderTypeLimit, "10", "95", ""))
	gone, _, _ := svc.PlaceOrder(ctx, 1, "", pending(domain.OrderSideBuy, domain.OrderTypeLimit, "1", "90", ""))
	if _, err := svc.CancelOrder(ctx, 1, gone.ID); err != nil {
		t.Fatal(err)
	}

	page, err := svc.ListOrders(ctx, 1, 20, "", domain.OrderStatusOpen)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != open.ID {
		t.Fatalf("ListOrders(OPEN) = %+v, %v; want only order %d (AC6)", page, err, open.ID)
	}
	page, err = svc.ListOrders(ctx, 1, 20, "", domain.OrderStatusFilled, domain.OrderStatusCancelled, domain.OrderStatusRejected)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != gone.ID {
		t.Fatalf("ListOrders(history) = %+v, %v; want only order %d", page, err, gone.ID)
	}
	page, err = svc.ListOrders(ctx, 1, 20, "")
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("ListOrders() = %+v, %v; want both orders without a filter", page, err)
	}
	for _, bad := range []domain.OrderStatus{"", "open", "PENDING"} {
		var verr *domain.ValidationError
		if _, err := svc.ListOrders(ctx, 1, 20, "", bad); !errors.As(err, &verr) {
			t.Errorf("ListOrders(%q) error = %v, want ValidationError", bad, err)
		}
	}
}
