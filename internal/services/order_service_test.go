package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/testutil"
)

// newTestOrderService builds an OrderService with the simulated latency disabled.
func newTestOrderService(repo domain.OrderRepository, provider domain.MarketDataProvider) *OrderService {
	s := NewOrderService(repo, provider)
	s.sleep = func(time.Duration) {}
	return s
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// fjrdProvider is SIM-FJRD pinned at 100: bid 99.90, ask 100.10. Other ids are unknown.
func fjrdProvider() *testutil.MarketProvider {
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

func buy(qty string) domain.OrderRequest {
	return domain.OrderRequest{OrderbookID: "SIM-FJRD", Side: domain.OrderSideBuy, Quantity: dec(qty)}
}

func TestOrderService_PlaceOrder_Validation(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		req     domain.OrderRequest
		wantMsg string
	}{
		{name: "blank orderbook_id", req: domain.OrderRequest{OrderbookID: "  ", Side: domain.OrderSideBuy, Quantity: dec("1")}, wantMsg: "orderbook_id is required"},
		{name: "side HOLD", req: domain.OrderRequest{OrderbookID: "SIM-FJRD", Side: "HOLD", Quantity: dec("1")}, wantMsg: "side must be BUY or SELL"},
		{name: "lowercase side", req: domain.OrderRequest{OrderbookID: "SIM-FJRD", Side: "buy", Quantity: dec("1")}, wantMsg: "side must be BUY or SELL"},
		{name: "quantity 0", req: buy("0"), wantMsg: "quantity must be greater than 0"},
		{name: "quantity -1", req: buy("-1"), wantMsg: "quantity must be greater than 0"},
		{name: "quantity 1.00001", req: buy("1.00001"), wantMsg: "quantity must have at most 4 decimals"},
		{name: "type LIMIT", req: domain.OrderRequest{OrderbookID: "SIM-FJRD", Side: domain.OrderSideBuy, Quantity: dec("1"), Type: "LIMIT"}, wantMsg: "type must be MARKET"},
		{name: "key with a space", key: "k 123", req: buy("1"), wantMsg: "Idempotency-Key must be printable ASCII without spaces"},
		{name: "key with non-ASCII", key: "k-ä", req: buy("1"), wantMsg: "Idempotency-Key must be printable ASCII without spaces"},
		{name: "key too long", key: strings.Repeat("k", domain.MaxIdempotencyKeyLength+1), req: buy("1"), wantMsg: "Idempotency-Key must be at most 255 characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			_, _, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, tt.key, tt.req)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error = %v, want *domain.ValidationError", err)
			}
			if verr.Msg != tt.wantMsg {
				t.Errorf("Msg = %q, want %q", verr.Msg, tt.wantMsg)
			}
			if len(repo.Executed) != 0 {
				t.Errorf("Execute called %d times, want 0", len(repo.Executed))
			}
		})
	}
}

func TestOrderService_PlaceOrder_AcceptsValidEdgeInputs(t *testing.T) {
	tests := []struct {
		name string
		key  string
		req  domain.OrderRequest
	}{
		{name: "4 decimals", req: buy("0.0001")},
		{name: "trailing zeros beyond 4 decimals", req: buy("1.000000")},
		{name: "explicit MARKET type", req: domain.OrderRequest{OrderbookID: "SIM-FJRD", Side: domain.OrderSideSell, Quantity: dec("1"), Type: domain.OrderTypeMarket}},
		{name: "padded orderbook_id", req: domain.OrderRequest{OrderbookID: " SIM-FJRD ", Side: domain.OrderSideBuy, Quantity: dec("1")}},
		{name: "longest key", key: strings.Repeat("~", domain.MaxIdempotencyKeyLength), req: buy("1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			order, created, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, tt.key, tt.req)
			if err != nil || !created {
				t.Fatalf("PlaceOrder() = created %v, error %v; want created, nil", created, err)
			}
			if order.OrderbookID != "SIM-FJRD" || order.Type != domain.OrderTypeMarket {
				t.Errorf("order = %+v, want SIM-FJRD MARKET", order)
			}
		})
	}
}

func TestOrderService_PlaceOrder_PricesAndExecutes(t *testing.T) {
	repo := &testutil.OrderRepo{}
	order, created, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 7, "k-1", buy("10"))
	if err != nil || !created {
		t.Fatalf("PlaceOrder() = created %v, error %v", created, err)
	}
	if len(repo.Executed) != 1 {
		t.Fatalf("Execute called %d times, want 1", len(repo.Executed))
	}
	p := repo.Executed[0]
	if p.UserID != 7 || p.StockID != 1 || p.Side != domain.OrderSideBuy || p.Type != domain.OrderTypeMarket || p.IdempotencyKey != "k-1" {
		t.Errorf("unexpected params: %+v", p)
	}
	for field, got := range map[string]struct{ got, want string }{
		"Quantity":   {p.Quantity.String(), "10"},
		"FillPrice":  {p.FillPrice.StringFixed(2), "100.10"},
		"Commission": {p.Commission.StringFixed(2), "2.50"},
		"Total":      {p.Total.StringFixed(2), "1003.50"},
	} {
		if got.got != got.want {
			t.Errorf("%s = %s, want %s", field, got.got, got.want)
		}
	}
	if order.Symbol != "FJRD" || order.Name != "Fjord Robotics" || order.Status != domain.OrderStatusFilled {
		t.Errorf("order = %+v, want FJRD / Fjord Robotics / FILLED", order)
	}
}

func TestOrderService_PlaceOrder_EnsuresStockFromDetails(t *testing.T) {
	var got [3]string
	repo := &testutil.OrderRepo{EnsureStockFn: func(_ context.Context, id, symbol, name string) (uint, error) {
		got = [3]string{id, symbol, name}
		return 9, nil
	}}
	if _, _, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, "", buy("1")); err != nil {
		t.Fatalf("PlaceOrder() error = %v", err)
	}
	if got != [3]string{"SIM-FJRD", "FJRD", "Fjord Robotics"} {
		t.Errorf("EnsureStock(%q, %q, %q), want SIM-FJRD, FJRD, Fjord Robotics", got[0], got[1], got[2])
	}
	if repo.Executed[0].StockID != 9 {
		t.Errorf("StockID = %d, want the id from EnsureStock (9)", repo.Executed[0].StockID)
	}
}

func TestOrderService_PlaceOrder_Slippage(t *testing.T) {
	tests := []struct {
		side      domain.OrderSide
		qty       string
		wantPrice string
	}{
		{domain.OrderSideBuy, "100", "100.10"},
		{domain.OrderSideBuy, "200", "100.60"},
		{domain.OrderSideSell, "100", "99.90"},
		{domain.OrderSideSell, "200", "99.40"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %s", tt.side, tt.qty), func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			req := domain.OrderRequest{OrderbookID: "SIM-FJRD", Side: tt.side, Quantity: dec(tt.qty)}
			if _, _, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, "", req); err != nil {
				t.Fatalf("PlaceOrder() error = %v", err)
			}
			if got := repo.Executed[0].FillPrice.StringFixed(2); got != tt.wantPrice {
				t.Errorf("FillPrice = %s, want %s", got, tt.wantPrice)
			}
		})
	}
}

func TestOrderService_PlaceOrder_SimulatesLatency(t *testing.T) {
	var slept []time.Duration
	svc := NewOrderService(&testutil.OrderRepo{}, fjrdProvider())
	svc.sleep = func(d time.Duration) { slept = append(slept, d) }

	for range 20 {
		if _, _, err := svc.PlaceOrder(context.Background(), 1, "", buy("1")); err != nil {
			t.Fatalf("PlaceOrder() error = %v", err)
		}
	}
	for _, d := range slept {
		if d < 50*time.Millisecond || d >= 500*time.Millisecond {
			t.Errorf("latency = %v, want within [50ms, 500ms)", d)
		}
	}
	if len(slept) != 20 {
		t.Errorf("slept %d times, want 20", len(slept))
	}
}

func TestOrderService_PlaceOrder_IdempotentReplay(t *testing.T) {
	repo := &testutil.OrderRepo{}
	var slept int
	svc := NewOrderService(repo, fjrdProvider())
	svc.sleep = func(time.Duration) { slept++ }

	first, created, err := svc.PlaceOrder(context.Background(), 1, "k-123", buy("10"))
	if err != nil || !created {
		t.Fatalf("first PlaceOrder() = created %v, error %v", created, err)
	}
	// The same payload, spelled differently (padded id, explicit type, 10.0), is the same order.
	again := domain.OrderRequest{OrderbookID: " SIM-FJRD", Side: domain.OrderSideBuy, Quantity: dec("10.0"), Type: domain.OrderTypeMarket}
	second, created, err := svc.PlaceOrder(context.Background(), 1, "k-123", again)
	if err != nil {
		t.Fatalf("replay PlaceOrder() error = %v", err)
	}
	if created {
		t.Error("replay created = true, want false")
	}
	if second.ID != first.ID || !second.Total.Equal(first.Total) {
		t.Errorf("replay = %+v, want the first order %+v", second, first)
	}
	if len(repo.Executed) != 1 {
		t.Errorf("Execute called %d times, want 1", len(repo.Executed))
	}
	if slept != 1 {
		t.Errorf("latency simulated %d times, want 1 (not on the replay)", slept)
	}

	t.Run("keys are per user", func(t *testing.T) {
		other, created, err := svc.PlaceOrder(context.Background(), 2, "k-123", buy("10"))
		if err != nil || !created || other.ID == first.ID {
			t.Errorf("other user's order = %+v, created %v, error %v; want a new order", other, created, err)
		}
	})
}

func TestOrderService_PlaceOrder_KeyReusedForDifferentOrder(t *testing.T) {
	differs := map[string]domain.OrderRequest{
		"orderbook_id": {OrderbookID: "SIM-OTHR", Side: domain.OrderSideBuy, Quantity: dec("10")},
		"side":         {OrderbookID: "SIM-FJRD", Side: domain.OrderSideSell, Quantity: dec("10")},
		"quantity":     buy("11"),
	}
	for field, req := range differs {
		t.Run("different "+field, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			svc := newTestOrderService(repo, fjrdProvider())
			if _, _, err := svc.PlaceOrder(context.Background(), 1, "k-123", buy("10")); err != nil {
				t.Fatalf("first PlaceOrder() error = %v", err)
			}
			_, _, err := svc.PlaceOrder(context.Background(), 1, "k-123", req)
			if !errors.Is(err, domain.ErrIdempotencyKeyReused) {
				t.Fatalf("error = %v, want ErrIdempotencyKeyReused", err)
			}
			if len(repo.Executed) != 1 {
				t.Errorf("Execute called %d times, want 1", len(repo.Executed))
			}
		})
	}
}

func TestOrderService_PlaceOrder_ConcurrentRetryResolvedByExecute(t *testing.T) {
	stored := &domain.Order{ID: 42, OrderbookID: "SIM-FJRD", Side: domain.OrderSideBuy, Type: domain.OrderTypeMarket,
		Status: domain.OrderStatusFilled, Quantity: dec("10")}
	newRepo := func() *testutil.OrderRepo {
		return &testutil.OrderRepo{ExecuteFn: func(context.Context, domain.ExecuteOrderParams) (*domain.Order, bool, error) {
			cp := *stored
			return &cp, false, nil
		}}
	}

	t.Run("same payload returns the stored order", func(t *testing.T) {
		order, created, err := newTestOrderService(newRepo(), fjrdProvider()).PlaceOrder(context.Background(), 1, "k-1", buy("10"))
		if err != nil || created || order.ID != 42 {
			t.Errorf("PlaceOrder() = %+v, created %v, error %v; want order 42, false, nil", order, created, err)
		}
	})
	t.Run("different payload is a reused key", func(t *testing.T) {
		_, _, err := newTestOrderService(newRepo(), fjrdProvider()).PlaceOrder(context.Background(), 1, "k-1", buy("5"))
		if !errors.Is(err, domain.ErrIdempotencyKeyReused) {
			t.Errorf("error = %v, want ErrIdempotencyKeyReused", err)
		}
	})
}

func TestOrderService_PlaceOrder_LookupErrors(t *testing.T) {
	dbErr := errors.New("db down")
	repo := &testutil.OrderRepo{FindByIdempotencyKeyFn: func(context.Context, uint, string) (*domain.Order, error) {
		return nil, dbErr
	}}
	_, _, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, "k-1", buy("1"))
	if !errors.Is(err, dbErr) {
		t.Fatalf("error = %v, want %v", err, dbErr)
	}
	if len(repo.Executed) != 0 {
		t.Error("Execute must not run when the key lookup fails")
	}
}

func TestOrderService_PlaceOrder_MarketDataErrors(t *testing.T) {
	upstream := errors.New("connection refused")
	tests := []struct {
		name    string
		details func(context.Context, string) (domain.InstrumentDetails, error)
		quote   func(context.Context, string) (domain.Quote, error)
		want    error
	}{
		{
			name: "unknown instrument in details",
			details: func(context.Context, string) (domain.InstrumentDetails, error) {
				return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
			},
			want: domain.ErrInstrumentNotFound,
		},
		{
			name: "unknown instrument in quote",
			quote: func(context.Context, string) (domain.Quote, error) {
				return domain.Quote{}, domain.ErrInstrumentNotFound
			},
			want: domain.ErrInstrumentNotFound,
		},
		{
			name: "details upstream failure",
			details: func(context.Context, string) (domain.InstrumentDetails, error) {
				return domain.InstrumentDetails{}, fmt.Errorf("%w: timeout", domain.ErrMarketDataUnavailable)
			},
			want: domain.ErrMarketDataUnavailable,
		},
		{
			name: "quote fails with an arbitrary error",
			quote: func(context.Context, string) (domain.Quote, error) {
				return domain.Quote{}, upstream
			},
			want: domain.ErrMarketDataUnavailable,
		},
		{
			name:  "zero bid",
			quote: testutil.FixedQuote(domain.Quote{Last: 100, Bid: 0, Ask: 100.10}),
			want:  domain.ErrMarketDataUnavailable,
		},
		{
			name:  "negative ask",
			quote: testutil.FixedQuote(domain.Quote{Last: 100, Bid: 99.90, Ask: -1}),
			want:  domain.ErrMarketDataUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := fjrdProvider()
			if tt.details != nil {
				provider.DetailsFn = tt.details
			}
			if tt.quote != nil {
				provider.QuoteFn = tt.quote
			}
			repo := &testutil.OrderRepo{}
			_, _, err := newTestOrderService(repo, provider).PlaceOrder(context.Background(), 1, "", buy("1"))
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if len(repo.Executed) != 0 {
				t.Errorf("Execute called %d times, want 0", len(repo.Executed))
			}
		})
	}
}

func TestOrderService_PlaceOrder_RepositoryErrorsPassThrough(t *testing.T) {
	for _, want := range []error{domain.ErrInsufficientFunds, domain.ErrInsufficientShares, errors.New("db down")} {
		t.Run(want.Error(), func(t *testing.T) {
			repo := &testutil.OrderRepo{ExecuteFn: func(context.Context, domain.ExecuteOrderParams) (*domain.Order, bool, error) {
				return nil, false, fmt.Errorf("execute: %w", want)
			}}
			order, created, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, "", buy("1"))
			if !errors.Is(err, want) || order != nil || created {
				t.Fatalf("PlaceOrder() = %v, %v, %v; want nil, false, %v", order, created, err, want)
			}
		})
	}

	t.Run("ensure stock failure", func(t *testing.T) {
		dbErr := errors.New("db down")
		repo := &testutil.OrderRepo{EnsureStockFn: func(context.Context, string, string, string) (uint, error) {
			return 0, dbErr
		}}
		_, _, err := newTestOrderService(repo, fjrdProvider()).PlaceOrder(context.Background(), 1, "", buy("1"))
		if !errors.Is(err, dbErr) {
			t.Fatalf("error = %v, want %v", err, dbErr)
		}
		if len(repo.Executed) != 0 {
			t.Error("Execute must not run when EnsureStock fails")
		}
	})
}
