package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/testutil"
)

// Story NW-08: the matching engine, on in-memory fakes with an injected clock, quote source and leader lock.

var engineClock = time.Date(2026, 11, 8, 9, 41, 3, 0, time.UTC)

func limitOrder(id uint, side domain.OrderSide, qty, limit string) domain.OpenOrder {
	return domain.OpenOrder{ID: id, UserID: 1, StockID: 1, OrderbookID: "SIM-KALL", Side: side,
		Type: domain.OrderTypeLimit, Quantity: dec(qty), LimitPrice: price(limit)}
}

func stopOrder(id uint, side domain.OrderSide, qty, stop string) domain.OpenOrder {
	return domain.OpenOrder{ID: id, UserID: 1, StockID: 1, OrderbookID: "SIM-KALL", Side: side,
		Type: domain.OrderTypeStop, Quantity: dec(qty), StopPrice: price(stop)}
}

// pinnedQuotes answers every instrument with the quote of a simulated pin at last (bid last × 0.999, ask last × 1.001).
func pinnedQuotes(bid, ask float64) *testutil.MarketProvider {
	return &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		return domain.Quote{OrderbookID: id, Bid: bid, Ask: ask}, nil
	}}
}

func newEngine(book *testutil.MatchingRepo, quotes domain.QuoteSource, lock *testutil.LeaderLock) *MatchingEngine {
	return NewMatchingEngine(book, quotes, lock, func() time.Time { return engineClock }, 4)
}

func TestTriggered(t *testing.T) {
	buy, sell := domain.OrderSideBuy, domain.OrderSideSell
	tests := []struct {
		name     string
		order    domain.OpenOrder
		bid, ask string
		want     bool
	}{
		// BUY LIMIT when ask ≤ limit_price.
		{"buy limit: ask below limit", limitOrder(1, buy, "10", "95"), "94.70", "94.89", true},
		{"buy limit: ask equals limit", limitOrder(1, buy, "10", "95"), "94.81", "95.00", true},
		{"buy limit: ask above limit", limitOrder(1, buy, "10", "95"), "99.90", "100.10", false},
		{"buy limit: only the bid below limit", limitOrder(1, buy, "10", "95"), "94.99", "95.01", false},
		// SELL LIMIT when bid ≥ limit_price.
		{"sell limit: bid above limit", limitOrder(1, sell, "10", "105"), "105.10", "105.30", true},
		{"sell limit: bid equals limit", limitOrder(1, sell, "10", "105"), "105.00", "105.21", true},
		{"sell limit: bid below limit", limitOrder(1, sell, "10", "105"), "104.99", "105.20", false},
		// SELL STOP when bid ≤ stop_price.
		{"sell stop: bid below stop", stopOrder(1, sell, "10", "90"), "88.91", "89.09", true},
		{"sell stop: bid equals stop", stopOrder(1, sell, "10", "90"), "90.00", "90.18", true},
		{"sell stop: bid above stop", stopOrder(1, sell, "10", "90"), "99.90", "100.10", false},
		// BUY STOP when ask ≥ stop_price.
		{"buy stop: ask above stop", stopOrder(1, buy, "10", "110"), "110.00", "110.22", true},
		{"buy stop: ask equals stop", stopOrder(1, buy, "10", "110"), "109.78", "110.00", true},
		{"buy stop: ask below stop", stopOrder(1, buy, "10", "110"), "99.90", "100.10", false},
		// Malformed orders never trigger.
		{"limit without limit_price", domain.OpenOrder{Side: buy, Type: domain.OrderTypeLimit}, "1", "1", false},
		{"stop without stop_price", domain.OpenOrder{Side: sell, Type: domain.OrderTypeStop}, "1", "1", false},
		{"market order", domain.OpenOrder{Side: buy, Type: domain.OrderTypeMarket, LimitPrice: price("100")}, "1", "1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Triggered(tt.order, dec(tt.bid), dec(tt.ask)); got != tt.want {
				t.Errorf("Triggered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchingEngine_SkipsCycleWithoutTheLock(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	quoted := 0
	quotes := &testutil.MarketProvider{QuoteFn: func(context.Context, string) (domain.Quote, error) {
		quoted++
		return domain.Quote{Bid: 1, Ask: 1}, nil
	}}
	lock := &testutil.LeaderLock{Held: false}

	res, err := newEngine(book, quotes, lock).RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}
	if !res.Skipped {
		t.Errorf("Skipped = false, want true without the lock")
	}
	if book.ListCalls != 0 || quoted != 0 || len(book.Fills) != 0 {
		t.Errorf("a follower must not read the book (%d), quote (%d) or fill (%d)", book.ListCalls, quoted, len(book.Fills))
	}
	if lock.Attempts != 1 {
		t.Errorf("TryAcquire calls = %d, want 1", lock.Attempts)
	}
	if got := book.Status(1); got != domain.OrderStatusOpen {
		t.Errorf("order status = %s, want OPEN", got)
	}
}

func TestMatchingEngine_LockErrorSkipsCycle(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	lock := &testutil.LeaderLock{TryAcquireFn: func(context.Context) (bool, error) { return false, errors.New("db down") }}
	res, err := newEngine(book, pinnedQuotes(1, 1), lock).RunCycle(context.Background())
	if err == nil || !res.Skipped {
		t.Fatalf("RunCycle() = %+v, %v; want skipped with an error", res, err)
	}
	if book.ListCalls != 0 {
		t.Errorf("ListOpen called without the lock")
	}
}

func TestMatchingEngine_TakesOverOnceTheLockIsFree(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	lock := &testutil.LeaderLock{Held: false}
	e := newEngine(book, pinnedQuotes(94.70, 94.89), lock)

	if res, _ := e.RunCycle(context.Background()); !res.Skipped {
		t.Fatalf("first cycle ran without the lock")
	}
	lock.Held = true // the other replica died and released it
	res, err := e.RunCycle(context.Background())
	if err != nil || res.Skipped || res.Filled != 1 {
		t.Fatalf("second cycle = %+v, %v; want 1 fill as leader", res, err)
	}
}

func TestMatchingEngine_FillsTriggeredOrdersWithMarketPricing(t *testing.T) {
	tests := []struct {
		name     string
		order    domain.OpenOrder
		bid, ask float64
		want     *domain.FillOrderParams // nil: stays OPEN
	}{
		{
			// AC2: SIM-KALL pinned at 94.80 → bid 94.71, ask 94.89; BUY LIMIT 10 @ 95 fills at the ask.
			name: "AC2 buy limit 10 @ 95 at ask 94.89", order: limitOrder(1, domain.OrderSideBuy, "10", "95"),
			bid: 94.71, ask: 94.89,
			want: &domain.FillOrderParams{OrderID: 1, FillPrice: dec("94.89"), Commission: dec("2.37"), Total: dec("951.27")},
		},
		{
			// AC5: pinned at 89 → bid 88.91; SELL STOP 10 @ 90 fills at the bid.
			name: "AC5 sell stop 10 @ 90 at bid 88.91", order: stopOrder(1, domain.OrderSideSell, "10", "90"),
			bid: 88.91, ask: 89.09,
			want: &domain.FillOrderParams{OrderID: 1, FillPrice: dec("88.91"), Commission: dec("2.22"), Total: dec("886.88")},
		},
		{
			name: "sell limit fills at the bid", order: limitOrder(1, domain.OrderSideSell, "4", "105"),
			bid: 105.50, ask: 105.70,
			// gross 422.00, commission max(1, 1.055) = 1.06, total 420.94
			want: &domain.FillOrderParams{OrderID: 1, FillPrice: dec("105.50"), Commission: dec("1.06"), Total: dec("420.94")},
		},
		{
			name: "buy stop fills at the ask", order: stopOrder(1, domain.OrderSideBuy, "1", "110"),
			bid: 110.88, ask: 111.12,
			// gross 111.12, commission max(1, 0.2778) = 1.00, total 112.12
			want: &domain.FillOrderParams{OrderID: 1, FillPrice: dec("111.12"), Commission: dec("1.00"), Total: dec("112.12")},
		},
		{
			name: "over 100 shares pays slippage like a market order", order: limitOrder(1, domain.OrderSideBuy, "200", "95"),
			bid: 94.00, ask: 94.20,
			// 94.20 × 1.005 = 94.671 → 94.67; gross 18934.00; commission 47.335 → 47.34; total 18981.34
			want: &domain.FillOrderParams{OrderID: 1, FillPrice: dec("94.67"), Commission: dec("47.34"), Total: dec("18981.34")},
		},
		{name: "AC1 not triggered at 100", order: limitOrder(1, domain.OrderSideBuy, "10", "95"), bid: 99.90, ask: 100.10},
		{name: "stop not triggered at 100", order: stopOrder(1, domain.OrderSideSell, "10", "90"), bid: 99.90, ask: 100.10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := testutil.NewMatchingRepo(tt.order)
			res, err := newEngine(book, pinnedQuotes(tt.bid, tt.ask), &testutil.LeaderLock{Held: true}).RunCycle(context.Background())
			if err != nil {
				t.Fatalf("RunCycle() error = %v", err)
			}
			if tt.want == nil {
				if len(book.Fills) != 0 || res.Filled != 0 || book.Status(1) != domain.OrderStatusOpen {
					t.Fatalf("order must stay OPEN, got fills %+v, status %s", book.Fills, book.Status(1))
				}
				return
			}
			if res.Filled != 1 || len(book.Fills) != 1 {
				t.Fatalf("filled = %d (%+v), want 1", res.Filled, book.Fills)
			}
			got := book.Fills[0]
			if got.OrderID != tt.want.OrderID || !got.FillPrice.Equal(tt.want.FillPrice) ||
				!got.Commission.Equal(tt.want.Commission) || !got.Total.Equal(tt.want.Total) {
				t.Errorf("fill = %s / %s / %s, want %s / %s / %s", got.FillPrice, got.Commission, got.Total,
					tt.want.FillPrice, tt.want.Commission, tt.want.Total)
			}
			if !got.FilledAt.Equal(engineClock) {
				t.Errorf("FilledAt = %v, want the injected clock %v", got.FilledAt, engineClock)
			}
			if book.Status(1) != domain.OrderStatusFilled {
				t.Errorf("status = %s, want FILLED", book.Status(1))
			}
		})
	}
}

func TestMatchingEngine_RejectsWhenFundsAreShortAtFillTime(t *testing.T) {
	tests := []struct {
		name       string
		fillErr    error
		wantReason string
	}{
		{"insufficient funds", domain.ErrInsufficientFunds, "insufficient funds at fill time: total 951.27 SEK exceeds buying power"},
		{"insufficient shares", domain.ErrInsufficientShares, "insufficient shares at fill time"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := testutil.NewMatchingRepo(limitOrder(7, domain.OrderSideBuy, "10", "95"))
			book.FillOrderFn = func(context.Context, domain.FillOrderParams) error { return tt.fillErr }

			res, err := newEngine(book, pinnedQuotes(94.71, 94.89), &testutil.LeaderLock{Held: true}).RunCycle(context.Background())
			if err != nil {
				t.Fatalf("RunCycle() error = %v", err)
			}
			if res.Rejected != 1 || res.Filled != 0 || res.Errors != 0 {
				t.Errorf("result = %+v, want 1 rejected", res)
			}
			if len(book.Rejections) != 1 || book.Rejections[0].OrderID != 7 || book.Rejections[0].Reason != tt.wantReason {
				t.Errorf("rejections = %+v, want order 7 with %q", book.Rejections, tt.wantReason)
			}
			if got := book.Status(7); got != domain.OrderStatusRejected {
				t.Errorf("status = %s, want REJECTED", got)
			}
		})
	}
}

func TestMatchingEngine_UnexpectedFillErrorIsRetriedNextCycle(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	failures := 1
	book.FillOrderFn = func(context.Context, domain.FillOrderParams) error {
		if failures > 0 {
			failures--
			return errors.New("serialization failure")
		}
		return nil
	}
	e := newEngine(book, pinnedQuotes(94.71, 94.89), &testutil.LeaderLock{Held: true})

	res, err := e.RunCycle(context.Background())
	if err != nil || res.Errors != 1 || res.Filled != 0 || book.Status(1) != domain.OrderStatusOpen || len(book.Rejections) != 0 {
		t.Fatalf("first cycle = %+v, %v, status %s; want 1 error, still OPEN, not rejected", res, err, book.Status(1))
	}
	res, err = e.RunCycle(context.Background())
	if err != nil || res.Filled != 1 {
		t.Fatalf("second cycle = %+v, %v; want the retry to fill", res, err)
	}
}

// The cancel-vs-fill race on one order: whichever transition runs first wins and the other sees ORDER_NOT_OPEN.
func TestMatchingEngine_CancelBeforeFillWins(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	orders := &testutil.OrderRepo{CancelFn: book.Cancel}
	svc := newTestOrderService(orders, pinnedQuotes(94.71, 94.89))

	// The engine listed the order, then the user's cancel committed before the fill transaction.
	book.ListOpenFn = func(context.Context) ([]domain.OpenOrder, error) {
		open := []domain.OpenOrder{limitOrder(1, domain.OrderSideBuy, "10", "95")}
		if _, err := svc.CancelOrder(context.Background(), 1, 1); err != nil {
			t.Fatalf("CancelOrder() error = %v", err)
		}
		return open, nil
	}
	res, err := newEngine(book, pinnedQuotes(94.71, 94.89), &testutil.LeaderLock{Held: true}).RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}
	if res.Filled != 0 || res.Errors != 0 || res.Rejected != 0 || len(book.Fills) != 0 {
		t.Errorf("result = %+v, fills %+v; the fill must lose quietly", res, book.Fills)
	}
	if got := book.Status(1); got != domain.OrderStatusCancelled {
		t.Errorf("status = %s, want CANCELLED", got)
	}
}

func TestMatchingEngine_FillBeforeCancelWins(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	orders := &testutil.OrderRepo{CancelFn: book.Cancel}
	svc := newTestOrderService(orders, pinnedQuotes(94.71, 94.89))

	if res, err := newEngine(book, pinnedQuotes(94.71, 94.89), &testutil.LeaderLock{Held: true}).RunCycle(context.Background()); err != nil || res.Filled != 1 {
		t.Fatalf("RunCycle() = %+v, %v; want 1 fill", res, err)
	}
	if _, err := svc.CancelOrder(context.Background(), 1, 1); !errors.Is(err, domain.ErrOrderNotOpen) {
		t.Errorf("CancelOrder() after the fill error = %v, want ErrOrderNotOpen", err)
	}
	if got := book.Status(1); got != domain.OrderStatusFilled {
		t.Errorf("status = %s, want FILLED", got)
	}
}

func TestMatchingEngine_ConcurrentCancelAndFillHaveExactlyOneWinner(t *testing.T) {
	for i := range 200 {
		book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
		svc := newTestOrderService(&testutil.OrderRepo{CancelFn: book.Cancel}, pinnedQuotes(94.71, 94.89))
		engine := newEngine(book, pinnedQuotes(94.71, 94.89), &testutil.LeaderLock{Held: true})

		var (
			wg        sync.WaitGroup
			res       CycleResult
			cycleErr  error
			cancelErr error
		)
		wg.Add(2)
		go func() { defer wg.Done(); res, cycleErr = engine.RunCycle(context.Background()) }()
		go func() { defer wg.Done(); _, cancelErr = svc.CancelOrder(context.Background(), 1, 1) }()
		wg.Wait()

		if cycleErr != nil || res.Errors != 0 {
			t.Fatalf("run %d: cycle = %+v, %v", i, res, cycleErr)
		}
		cancelled := cancelErr == nil
		if cancelErr != nil && !errors.Is(cancelErr, domain.ErrOrderNotOpen) {
			t.Fatalf("run %d: cancel error = %v, want nil or ErrOrderNotOpen", i, cancelErr)
		}
		filled := res.Filled == 1
		if cancelled == filled {
			t.Fatalf("run %d: cancelled=%v filled=%v, want exactly one winner", i, cancelled, filled)
		}
		want := domain.OrderStatusFilled
		if cancelled {
			want = domain.OrderStatusCancelled
		}
		if got := book.Status(1); got != want {
			t.Fatalf("run %d: status = %s, want %s", i, got, want)
		}
	}
}

func TestMatchingEngine_QuotesEachInstrumentOnceWithABoundedPool(t *testing.T) {
	var orders []domain.OpenOrder
	instruments := []string{"SIM-A", "SIM-B", "SIM-C", "SIM-D", "SIM-E", "SIM-F", "SIM-G"}
	id := uint(1)
	for _, inst := range instruments {
		for range 3 {
			o := limitOrder(id, domain.OrderSideBuy, "1", "95")
			o.OrderbookID = inst
			orders = append(orders, o)
			id++
		}
	}
	book := testutil.NewMatchingRepo(orders...)

	var (
		mu               sync.Mutex
		calls            = map[string]int{}
		inFlight, maxPar int32
	)
	quotes := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		n := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)
		for {
			m := atomic.LoadInt32(&maxPar)
			if n <= m || atomic.CompareAndSwapInt32(&maxPar, m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		calls[id]++
		mu.Unlock()
		if id == "SIM-C" {
			return domain.Quote{}, domain.ErrMarketDataUnavailable
		}
		if id == "SIM-D" {
			return domain.Quote{Bid: 0, Ask: 0}, nil // no valid bid/ask
		}
		return domain.Quote{Bid: 94, Ask: 94.5}, nil
	}}

	engine := NewMatchingEngine(book, quotes, &testutil.LeaderLock{Held: true}, nil, 2)
	res, err := engine.RunCycle(context.Background())
	if err != nil {
		t.Fatalf("RunCycle() error = %v", err)
	}
	for _, inst := range instruments {
		if calls[inst] != 1 {
			t.Errorf("%s quoted %d times, want once per cycle", inst, calls[inst])
		}
	}
	if maxPar > 2 {
		t.Errorf("max concurrent quotes = %d, want at most 2 workers", maxPar)
	}
	// 5 good instruments × 3 orders fill; SIM-C and SIM-D are counted as errors and stay OPEN.
	if res.Open != 21 || res.Filled != 15 || res.Errors != 2 {
		t.Errorf("result = %+v, want 21 open, 15 filled, 2 errors", res)
	}
	for _, o := range orders {
		want := domain.OrderStatusFilled
		if o.OrderbookID == "SIM-C" || o.OrderbookID == "SIM-D" {
			want = domain.OrderStatusOpen
		}
		if got := book.Status(o.ID); got != want {
			t.Errorf("order %d (%s) status = %s, want %s", o.ID, o.OrderbookID, got, want)
		}
	}
}

func TestMatchingEngine_EmptyBookQuotesNothing(t *testing.T) {
	quotes := &testutil.MarketProvider{QuoteFn: func(context.Context, string) (domain.Quote, error) {
		t.Error("Quote called with no open orders")
		return domain.Quote{}, nil
	}}
	res, err := newEngine(testutil.NewMatchingRepo(), quotes, &testutil.LeaderLock{Held: true}).RunCycle(context.Background())
	if err != nil || res.Skipped || res.Open != 0 {
		t.Errorf("RunCycle() = %+v, %v", res, err)
	}
}

func TestMatchingEngine_ListOpenErrorFailsTheCycle(t *testing.T) {
	book := testutil.NewMatchingRepo()
	book.ListOpenFn = func(context.Context) ([]domain.OpenOrder, error) { return nil, errors.New("db down") }
	if _, err := newEngine(book, pinnedQuotes(1, 1), &testutil.LeaderLock{Held: true}).RunCycle(context.Background()); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Errorf("RunCycle() error = %v, want the list error", err)
	}
}

func TestMatchingEngine_RunStopsOnContextAndReleasesTheLock(t *testing.T) {
	book := testutil.NewMatchingRepo(limitOrder(1, domain.OrderSideBuy, "10", "95"))
	lock := &testutil.LeaderLock{Held: true}
	e := newEngine(book, pinnedQuotes(94.71, 94.89), lock)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.Run(ctx, 5*time.Millisecond) }()

	deadline := time.After(2 * time.Second)
	for book.Status(1) != domain.OrderStatusFilled {
		select {
		case <-deadline:
			t.Fatal("Run did not fill the order within 2s")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
	if lock.Releases != 1 {
		t.Errorf("Release calls = %d, want 1 on shutdown", lock.Releases)
	}
}
