package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/testutil"
)

// Story NW-09: QuoteHub fan-out. The pollers only tick when a test calls ManualTicker.Tick, and prices come
// from a testutil.QuoteBoard, so every test is deterministic.

func quoteOf(id string, last float64) domain.Quote {
	return domain.Quote{
		OrderbookID: id, Symbol: id, Last: last, Bid: domain.RoundPrice(last * 0.999), Ask: domain.RoundPrice(last * 1.001),
		Currency: "SEK", UpdatedAt: time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC),
	}
}

type hubFixture struct {
	hub     *QuoteHub
	board   *testutil.QuoteBoard
	tickers *testutil.ManualTickers
	mu      sync.Mutex
	logs    []string
}

func newHubFixture(t *testing.T, quotes ...domain.Quote) *hubFixture {
	t.Helper()
	f := &hubFixture{board: testutil.NewQuoteBoard(quotes...), tickers: &testutil.ManualTickers{}}
	f.hub = NewQuoteHub(f.board.Provider(), QuoteHubOptions{
		Interval:  time.Second,
		NewTicker: func(d time.Duration) Ticker { return f.tickers.New(d) },
		Logf: func(format string, _ ...any) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.logs = append(f.logs, format)
		},
	})
	t.Cleanup(f.hub.Close)
	return f
}

func (f *hubFixture) logCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.logs)
}

// tick ticks the only poller of the hub (the tests that use it subscribe to a single instrument).
func (f *hubFixture) tick(t *testing.T, n int) {
	t.Helper()
	tickers := f.tickers.All()
	if len(tickers) != n {
		t.Fatalf("tickers created = %d, want %d", len(tickers), n)
	}
	if err := tickers[n-1].Tick(); err != nil {
		t.Fatal(err)
	}
}

func receive(t *testing.T, ch <-chan domain.Quote) domain.Quote {
	t.Helper()
	select {
	case q, ok := <-ch:
		if !ok {
			t.Fatal("channel closed, want a quote")
		}
		return q
	case <-time.After(time.Second):
		t.Fatal("no quote within 1s")
	}
	return domain.Quote{}
}

func expectNothing(t *testing.T, ch <-chan domain.Quote) {
	t.Helper()
	select {
	case q, ok := <-ch:
		if ok {
			t.Fatalf("unexpected quote %+v", q)
		}
		t.Fatal("channel closed, want it open and empty")
	case <-time.After(20 * time.Millisecond):
	}
}

func expectClosed(t *testing.T, ch <-chan domain.Quote) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel not closed within 1s")
		}
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestQuoteHub_SubscribeSendsCurrentQuotesFirst(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100), quoteOf("B", 50))
	ch, err := f.hub.Subscribe(context.Background(), []string{"A", "B"})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	got := map[string]float64{}
	for range 2 {
		q := receive(t, ch)
		got[q.OrderbookID] = q.Last
	}
	if got["A"] != 100 || got["B"] != 50 {
		t.Errorf("initial quotes = %v, want A 100 and B 50", got)
	}
	if f.hub.Pollers() != 2 {
		t.Errorf("Pollers() = %d, want 2 (one per instrument)", f.hub.Pollers())
	}
	for _, d := range f.tickers.Intervals() {
		if d != time.Second {
			t.Errorf("ticker interval = %s, want the configured 1s", d)
		}
	}
}

func TestQuoteHub_TwoSubscribersShareOnePoller(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100))
	ctx := context.Background()
	ch1, err := f.hub.Subscribe(ctx, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := f.hub.Subscribe(ctx, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	if n := f.hub.Pollers(); n != 1 {
		t.Fatalf("Pollers() = %d, want 1 for two subscribers of one instrument", n)
	}
	if n := len(f.tickers.All()); n != 1 {
		t.Fatalf("tickers created = %d, want 1", n)
	}
	if n := f.board.Calls("A"); n != 1 {
		t.Errorf("upstream calls on subscribe = %d, want 1 (the second subscriber reuses the poller's quote)", n)
	}
	for _, ch := range []<-chan domain.Quote{ch1, ch2} {
		if q := receive(t, ch); q.Last != 100 {
			t.Errorf("initial last = %v, want 100", q.Last)
		}
	}

	f.board.Set(quoteOf("A", 123.45))
	f.tick(t, 1)
	for _, ch := range []<-chan domain.Quote{ch1, ch2} {
		if q := receive(t, ch); q.Last != 123.45 {
			t.Errorf("pushed last = %v, want 123.45", q.Last)
		}
	}
	if n := f.board.Calls("A"); n != 2 {
		t.Errorf("upstream calls after one tick = %d, want 2 (one poll serves both subscribers)", n)
	}
}

func TestQuoteHub_UnsubscribeStopsPollerWithLastSubscriber(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100))
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ch1, err := f.hub.Subscribe(ctx1, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := f.hub.Subscribe(ctx2, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	ticker := f.tickers.All()[0]

	cancel1()
	expectClosed(t, ch1)
	if n := f.hub.Pollers(); n != 1 {
		t.Fatalf("Pollers() after one of two left = %d, want 1", n)
	}
	select {
	case <-ticker.Stopped():
		t.Fatal("poller stopped while a subscriber is left")
	default:
	}

	cancel2()
	expectClosed(t, ch2)
	waitUntil(t, "no pollers", func() bool { return f.hub.Pollers() == 0 })
	select {
	case <-ticker.Stopped():
	case <-time.After(time.Second):
		t.Fatal("poller's ticker not stopped after the last subscriber left")
	}

	// A new subscriber starts a fresh poller with a fresh quote.
	f.board.Set(quoteOf("A", 101))
	ch3, err := f.hub.Subscribe(context.Background(), []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	if q := receive(t, ch3); q.Last != 101 {
		t.Errorf("resubscribe initial last = %v, want the fresh 101", q.Last)
	}
	if n := len(f.tickers.All()); n != 2 {
		t.Errorf("tickers created = %d, want 2 (a new poller)", n)
	}
}

func TestQuoteHub_EmitsOnlyChangedQuotes(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100))
	ch, err := f.hub.Subscribe(context.Background(), []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, ch)

	// Same prices, newer updated_at: not a change.
	same := quoteOf("A", 100)
	same.UpdatedAt = same.UpdatedAt.Add(time.Minute)
	f.board.Set(same)
	f.tick(t, 1)
	f.tick(t, 1)

	// Only change_percent moves: a change.
	moved := quoteOf("A", 100)
	moved.ChangePercent = 1.5
	f.board.Set(moved)
	f.tick(t, 1)
	if q := receive(t, ch); q.ChangePercent != 1.5 {
		t.Errorf("first pushed quote = %+v, want the change_percent 1.5 one (unchanged polls must not emit)", q)
	}
	f.tick(t, 1)
	expectNothing(t, ch)

	f.board.Set(quoteOf("A", 99))
	f.tick(t, 1)
	if q := receive(t, ch); q.Last != 99 {
		t.Errorf("pushed last = %v, want 99", q.Last)
	}
}

func TestQuoteHub_SlowConsumerKeepsLatestPerInstrumentAndDoesNotBlockOthers(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100))
	ctx := context.Background()
	slow, err := f.hub.Subscribe(ctx, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := f.hub.Subscribe(ctx, []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, fast)

	// The slow subscriber never reads; the fast one must still get every change.
	for i := 1; i <= 5; i++ {
		f.board.Set(quoteOf("A", 100+float64(i)))
		f.tick(t, 1)
		if q := receive(t, fast); q.Last != 100+float64(i) {
			t.Fatalf("fast subscriber got %v, want %v", q.Last, 100+float64(i))
		}
	}
	if q := receive(t, slow); q.Last != 105 {
		t.Errorf("slow subscriber got %v, want only the latest 105 (older quotes dropped)", q.Last)
	}
	expectNothing(t, slow)
}

func TestQuoteSubscriber_DeliverCoalescesPerInstrument(t *testing.T) {
	tests := []struct {
		name    string
		ids     []string
		deliver []domain.Quote
		want    []domain.Quote
	}{
		{
			name:    "fits the buffer",
			ids:     []string{"A", "B"},
			deliver: []domain.Quote{quoteOf("A", 1), quoteOf("B", 1)},
			want:    []domain.Quote{quoteOf("A", 1), quoteOf("B", 1)},
		},
		{
			name:    "full: the older quote of the same instrument is dropped, other instruments keep theirs",
			ids:     []string{"A", "B"},
			deliver: []domain.Quote{quoteOf("A", 1), quoteOf("B", 1), quoteOf("A", 2)},
			want:    []domain.Quote{quoteOf("B", 1), quoteOf("A", 2)},
		},
		{
			name:    "many updates of one instrument",
			ids:     []string{"A", "B"},
			deliver: []domain.Quote{quoteOf("A", 1), quoteOf("B", 1), quoteOf("A", 2), quoteOf("B", 2), quoteOf("A", 3)},
			want:    []domain.Quote{quoteOf("B", 2), quoteOf("A", 3)},
		},
		{
			name:    "single instrument keeps only the latest",
			ids:     []string{"A"},
			deliver: []domain.Quote{quoteOf("A", 1), quoteOf("A", 2), quoteOf("A", 3)},
			want:    []domain.Quote{quoteOf("A", 3)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &quoteSubscriber{ids: tt.ids, ch: make(chan domain.Quote, len(tt.ids))}
			for _, q := range tt.deliver {
				s.deliver(q)
			}
			s.close()
			var got []domain.Quote
			for q := range s.ch {
				got = append(got, q)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d quotes %+v, want %+v", len(got), got, tt.want)
			}
			for i := range got {
				if got[i].OrderbookID != tt.want[i].OrderbookID || got[i].Last != tt.want[i].Last {
					t.Errorf("quote %d = %s %v, want %s %v", i, got[i].OrderbookID, got[i].Last, tt.want[i].OrderbookID, tt.want[i].Last)
				}
			}
			s.deliver(quoteOf("A", 9)) // after close: a no-op, no panic
		})
	}
}

func TestQuoteHub_SubscribeErrorsLeaveNothingBehind(t *testing.T) {
	tests := []struct {
		name    string
		ids     []string
		setup   func(b *testutil.QuoteBoard)
		wantErr error
	}{
		{name: "unknown id", ids: []string{"A", "NOPE"}, wantErr: domain.ErrInstrumentNotFound},
		{name: "unknown id wins over a later upstream failure", ids: []string{"NOPE", "A"},
			setup: func(b *testutil.QuoteBoard) { b.Fail("A", domain.ErrMarketDataUnavailable) }, wantErr: domain.ErrInstrumentNotFound},
		{name: "upstream down", ids: []string{"A"},
			setup: func(b *testutil.QuoteBoard) { b.Fail("A", domain.ErrMarketDataUnavailable) }, wantErr: domain.ErrMarketDataUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newHubFixture(t, quoteOf("A", 100))
			if tt.setup != nil {
				tt.setup(f.board)
			}
			ch, err := f.hub.Subscribe(context.Background(), tt.ids)
			if !errors.Is(err, tt.wantErr) || ch != nil {
				t.Fatalf("Subscribe() = %v, %v; want nil, %v", ch, err, tt.wantErr)
			}
			if n := f.hub.Pollers(); n != 0 {
				t.Errorf("Pollers() = %d, want 0 after a failed subscribe", n)
			}
		})
	}

	t.Run("no ids", func(t *testing.T) {
		f := newHubFixture(t)
		_, err := f.hub.Subscribe(context.Background(), nil)
		expectValidationError(t, err)
	})
}

func TestQuoteHub_PollFailureKeepsLastQuoteAndLogsOnce(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100))
	ch, err := f.hub.Subscribe(context.Background(), []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	receive(t, ch)

	f.board.Fail("A", domain.ErrMarketDataUnavailable)
	f.tick(t, 1)
	f.tick(t, 1)
	f.tick(t, 1)
	expectNothing(t, ch)
	waitUntil(t, "one failure log", func() bool { return f.logCount() == 1 })

	f.board.Fail("A", nil)
	f.board.Set(quoteOf("A", 110))
	f.tick(t, 1)
	if q := receive(t, ch); q.Last != 110 {
		t.Errorf("after recovery last = %v, want 110", q.Last)
	}
	if n := f.logCount(); n != 2 {
		t.Errorf("logs = %d, want 2 (one failure, one recovery), not one per failed poll", n)
	}
}

func TestQuoteHub_CloseEndsEverything(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100), quoteOf("B", 50))
	ch1, err := f.hub.Subscribe(context.Background(), []string{"A"})
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := f.hub.Subscribe(context.Background(), []string{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	f.hub.Close()
	expectClosed(t, ch1)
	expectClosed(t, ch2)
	if n := f.hub.Pollers(); n != 0 {
		t.Errorf("Pollers() after Close = %d, want 0", n)
	}
	for i, tk := range f.tickers.All() {
		select {
		case <-tk.Stopped():
		default:
			t.Errorf("ticker %d not stopped after Close (Close waits for the pollers)", i)
		}
	}
	if _, err := f.hub.Subscribe(context.Background(), []string{"A"}); !errors.Is(err, domain.ErrMarketDataUnavailable) {
		t.Errorf("Subscribe() after Close error = %v, want ErrMarketDataUnavailable", err)
	}
	f.hub.Close() // idempotent
}

func TestQuoteHub_ConcurrentSubscribersAreRaceFree(t *testing.T) {
	f := newHubFixture(t, quoteOf("A", 100), quoteOf("B", 50))
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			ids := []string{"A"}
			if i%2 == 0 {
				ids = []string{"A", "B"}
			}
			ch, err := f.hub.Subscribe(ctx, ids)
			if err != nil {
				t.Error(err)
				cancel()
				return
			}
			<-ch
			cancel()
			for range ch {
			}
		}()
	}
	wg.Wait()
	waitUntil(t, "every poller stopped", func() bool { return f.hub.Pollers() == 0 })
}
