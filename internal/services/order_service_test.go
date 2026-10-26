package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
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

// ---------------------------------------------------------------------------
// ListOrders
// ---------------------------------------------------------------------------

// keysetOrders returns a ListByUserFn over a fixed set of orders per user that follows the
// domain.OrderRepository contract (newest first by created_at desc, id desc; strictly after before; at
// most limit). It records every requested limit in limits.
func keysetOrders(byUser map[uint][]domain.Order, limits *[]int) func(context.Context, uint, *domain.OrderCursor, int) ([]domain.Order, error) {
	return func(_ context.Context, userID uint, before *domain.OrderCursor, limit int) ([]domain.Order, error) {
		if limits != nil {
			*limits = append(*limits, limit)
		}
		items := append([]domain.Order(nil), byUser[userID]...)
		newer := func(a, b domain.Order) bool {
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.ID > b.ID
		}
		sort.Slice(items, func(i, j int) bool { return newer(items[i], items[j]) })
		page := []domain.Order{}
		for _, o := range items {
			if before != nil && !newer(domain.Order{CreatedAt: before.CreatedAt, ID: before.ID}, o) {
				continue
			}
			if len(page) == limit {
				break
			}
			page = append(page, o)
		}
		return page, nil
	}
}

var listBase = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// orderAt is an order with id created minute minutes plus micro microseconds after listBase.
func orderAt(id uint, minute int, micro int) domain.Order {
	return domain.Order{ID: id, OrderbookID: "SIM-FJRD", CreatedAt: listBase.Add(time.Duration(minute)*time.Minute + time.Duration(micro)*time.Microsecond)}
}

func ids(orders []domain.Order) []uint {
	out := make([]uint, len(orders))
	for i, o := range orders {
		out[i] = o.ID
	}
	return out
}

func equalIDs(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func listService(byUser map[uint][]domain.Order, limits *[]int) *OrderService {
	return newTestOrderService(&testutil.OrderRepo{ListByUserFn: keysetOrders(byUser, limits)}, fjrdProvider())
}

func TestOrderService_ListOrders_Empty(t *testing.T) {
	page, err := newTestOrderService(&testutil.OrderRepo{}, fjrdProvider()).ListOrders(context.Background(), 1, domain.DefaultOrderPageLimit, "")
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.NextCursor != nil {
		t.Errorf("page = %+v, want non-nil empty items and nil cursor", page)
	}
}

func TestOrderService_ListOrders_ExactlyLimit(t *testing.T) {
	var limits []int
	svc := listService(map[uint][]domain.Order{1: {orderAt(1, 1, 0), orderAt(2, 2, 0), orderAt(3, 3, 0)}}, &limits)
	page, err := svc.ListOrders(context.Background(), 1, 3, "")
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if got := ids(page.Items); !equalIDs(got, []uint{3, 2, 1}) {
		t.Errorf("ids = %v, want [3 2 1]", got)
	}
	if page.NextCursor != nil {
		t.Errorf("NextCursor = %q, want nil on the last page", *page.NextCursor)
	}
	if len(limits) != 1 || limits[0] != 4 {
		t.Errorf("repository limits = %v, want [4] (limit+1)", limits)
	}
}

func TestOrderService_ListOrders_PagesWithCursor(t *testing.T) {
	// Microsecond timestamps as Postgres stores them; the cursor must keep them exact.
	orders := []domain.Order{orderAt(1, 1, 111111), orderAt(2, 2, 222222), orderAt(3, 3, 333333), orderAt(4, 4, 444444), orderAt(5, 5, 555555)}
	svc := listService(map[uint][]domain.Order{1: orders}, nil)
	ctx := context.Background()

	first, err := svc.ListOrders(ctx, 1, 2, "")
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	if got := ids(first.Items); !equalIDs(got, []uint{5, 4}) || first.NextCursor == nil {
		t.Fatalf("first page = %v cursor %v, want [5 4] with a cursor", got, first.NextCursor)
	}
	wantCursor := encodeOrderCursor(domain.OrderCursor{CreatedAt: orders[3].CreatedAt, ID: 4})
	if *first.NextCursor != wantCursor {
		t.Errorf("NextCursor = %q, want the cursor of the last item %q", *first.NextCursor, wantCursor)
	}

	second, err := svc.ListOrders(ctx, 1, 2, *first.NextCursor)
	if err != nil {
		t.Fatalf("second page error = %v", err)
	}
	if got := ids(second.Items); !equalIDs(got, []uint{3, 2}) || second.NextCursor == nil {
		t.Fatalf("second page = %v cursor %v, want [3 2] with a cursor", got, second.NextCursor)
	}

	third, err := svc.ListOrders(ctx, 1, 2, *second.NextCursor)
	if err != nil {
		t.Fatalf("third page error = %v", err)
	}
	if got := ids(third.Items); !equalIDs(got, []uint{1}) || third.NextCursor != nil {
		t.Errorf("third page = %v cursor %v, want [1] without a cursor", got, third.NextCursor)
	}
}

func TestOrderService_ListOrders_EqualCreatedAtOrderedByID(t *testing.T) {
	// Four orders in the same microsecond: the id breaks the tie, also across a page boundary.
	same := []domain.Order{orderAt(10, 0, 7), orderAt(11, 0, 7), orderAt(12, 0, 7), orderAt(13, 0, 7), orderAt(9, -1, 0)}
	svc := listService(map[uint][]domain.Order{1: same}, nil)
	var got []uint
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := svc.ListOrders(context.Background(), 1, 2, cursor)
		if err != nil {
			t.Fatalf("ListOrders() error = %v", err)
		}
		got = append(got, ids(page.Items)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !equalIDs(got, []uint{13, 12, 11, 10, 9}) {
		t.Errorf("ids across pages = %v, want [13 12 11 10 9]", got)
	}
}

func TestOrderService_ListOrders_OnlyOwnOrders(t *testing.T) {
	repo := &testutil.OrderRepo{}
	svc := newTestOrderService(repo, fjrdProvider())
	ctx := context.Background()
	for _, userID := range []uint{1, 2, 1} {
		if _, _, err := svc.PlaceOrder(ctx, userID, "", buy("1")); err != nil {
			t.Fatalf("PlaceOrder() error = %v", err)
		}
	}

	mine, err := svc.ListOrders(ctx, 1, domain.MaxOrderPageLimit, "")
	if err != nil {
		t.Fatalf("ListOrders(user 1) error = %v", err)
	}
	if got := ids(mine.Items); !equalIDs(got, []uint{3, 1}) || mine.NextCursor != nil {
		t.Errorf("user 1 page = %v cursor %v, want [3 1] without a cursor", got, mine.NextCursor)
	}

	other, err := svc.ListOrders(ctx, 3, domain.DefaultOrderPageLimit, "")
	if err != nil {
		t.Fatalf("ListOrders(user 3) error = %v", err)
	}
	if other.Items == nil || len(other.Items) != 0 || other.NextCursor != nil {
		t.Errorf("user 3 page = %+v, want empty", other)
	}

	// A cursor taken from user 1's list does not reveal another user's orders.
	page, err := svc.ListOrders(ctx, 1, 1, "")
	if err != nil || page.NextCursor == nil {
		t.Fatalf("ListOrders(user 1, limit 1) = %+v, %v", page, err)
	}
	foreign, err := svc.ListOrders(ctx, 3, 1, *page.NextCursor)
	if err != nil || len(foreign.Items) != 0 {
		t.Errorf("user 3 with user 1's cursor = %+v, %v; want empty", foreign, err)
	}
}

func TestOrderService_ListOrders_InvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		limit   int
		cursor  string
		wantMsg string
	}{
		{name: "limit 0", limit: 0, wantMsg: "limit must be an integer from 1 to 100"},
		{name: "limit -1", limit: -1, wantMsg: "limit must be an integer from 1 to 100"},
		{name: "limit 101", limit: domain.MaxOrderPageLimit + 1, wantMsg: "limit must be an integer from 1 to 100"},
		{name: "garbage cursor", limit: 20, cursor: "garbage!", wantMsg: "cursor is invalid"},
		{name: "cursor with id 0", limit: 20, cursor: "eyJjIjoiMjAyNi0xMC0yNlQwOTo0MjowMFoiLCJpIjowfQ", wantMsg: "cursor is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			repo := &testutil.OrderRepo{ListByUserFn: func(context.Context, uint, *domain.OrderCursor, int) ([]domain.Order, error) {
				called = true
				return []domain.Order{}, nil
			}}
			_, err := newTestOrderService(repo, fjrdProvider()).ListOrders(context.Background(), 1, tt.limit, tt.cursor)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("error = %v, want *domain.ValidationError", err)
			}
			if verr.Msg != tt.wantMsg {
				t.Errorf("Msg = %q, want %q", verr.Msg, tt.wantMsg)
			}
			if called {
				t.Error("the repository must not be called for invalid input")
			}
		})
	}
}

func TestOrderService_ListOrders_LimitBounds(t *testing.T) {
	for _, limit := range []int{1, domain.MaxOrderPageLimit} {
		var limits []int
		if _, err := listService(nil, &limits).ListOrders(context.Background(), 1, limit, ""); err != nil {
			t.Errorf("ListOrders(limit %d) error = %v", limit, err)
		}
		if len(limits) != 1 || limits[0] != limit+1 {
			t.Errorf("limit %d: repository limits = %v, want [%d]", limit, limits, limit+1)
		}
	}
}

func TestOrderService_ListOrders_RepositoryError(t *testing.T) {
	boom := errors.New("db down")
	repo := &testutil.OrderRepo{ListByUserFn: func(context.Context, uint, *domain.OrderCursor, int) ([]domain.Order, error) {
		return nil, boom
	}}
	_, err := newTestOrderService(repo, fjrdProvider()).ListOrders(context.Background(), 1, 20, "")
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want wrapping %v", err, boom)
	}
}
