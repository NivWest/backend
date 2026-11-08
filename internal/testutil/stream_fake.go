package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"backend/internal/domain"
)

// ---------------------------------------------------------------------------
// Live quotes (story NW-09)
// ---------------------------------------------------------------------------

// ManualTicker is a fake ticker (services.Ticker) that only ticks when the test calls Tick.
type ManualTicker struct {
	ch       chan time.Time
	stopOnce sync.Once
	stopped  chan struct{}
}

func (t *ManualTicker) C() <-chan time.Time { return t.ch }

func (t *ManualTicker) Stop() { t.stopOnce.Do(func() { close(t.stopped) }) }

// Stopped is closed once the ticker's owner called Stop.
func (t *ManualTicker) Stopped() <-chan struct{} { return t.stopped }

// Tick delivers one tick, waiting until the owner receives it. It fails after a second (owner gone).
func (t *ManualTicker) Tick() error {
	select {
	case t.ch <- time.Now():
		return nil
	case <-t.stopped:
		return fmt.Errorf("testutil: tick on a stopped ticker")
	case <-time.After(time.Second):
		return fmt.Errorf("testutil: nobody received the tick")
	}
}

// ManualTickers creates ManualTickers and remembers them in creation order. Its New method is a
// services.QuoteHubOptions.NewTicker (wrap it: func(d) services.Ticker { return m.New(d) }).
type ManualTickers struct {
	mu        sync.Mutex
	tickers   []*ManualTicker
	intervals []time.Duration
}

func (m *ManualTickers) New(d time.Duration) *ManualTicker {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &ManualTicker{ch: make(chan time.Time), stopped: make(chan struct{})}
	m.tickers = append(m.tickers, t)
	m.intervals = append(m.intervals, d)
	return t
}

// All returns every ticker created so far.
func (m *ManualTickers) All() []*ManualTicker {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*ManualTicker(nil), m.tickers...)
}

// Intervals returns the interval each ticker was created with.
func (m *ManualTickers) Intervals() []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]time.Duration(nil), m.intervals...)
}

// QuoteBoard is a mutable set of quotes for a MarketProvider: Quote returns the current quote of an id,
// ErrInstrumentNotFound for an unknown one, or the error set with Fail. It counts the calls per id.
type QuoteBoard struct {
	mu     sync.Mutex
	quotes map[string]domain.Quote
	fail   map[string]error
	calls  map[string]int
}

// NewQuoteBoard returns a board holding quotes (keyed by their OrderbookID).
func NewQuoteBoard(quotes ...domain.Quote) *QuoteBoard {
	b := &QuoteBoard{quotes: map[string]domain.Quote{}, fail: map[string]error{}, calls: map[string]int{}}
	for _, q := range quotes {
		b.quotes[q.OrderbookID] = q
	}
	return b
}

// Set replaces the quote of q.OrderbookID.
func (b *QuoteBoard) Set(q domain.Quote) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.quotes[q.OrderbookID] = q
}

// Fail makes Quote(id) return err until Fail(id, nil).
func (b *QuoteBoard) Fail(id string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		delete(b.fail, id)
		return
	}
	b.fail[id] = err
}

// Calls returns how often Quote(id) was called.
func (b *QuoteBoard) Calls(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[id]
}

// Quote is a MarketProvider.QuoteFn.
func (b *QuoteBoard) Quote(_ context.Context, id string) (domain.Quote, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls[id]++
	if err := b.fail[id]; err != nil {
		return domain.Quote{}, err
	}
	q, ok := b.quotes[id]
	if !ok {
		return domain.Quote{}, fmt.Errorf("testutil: %q: %w", id, domain.ErrInstrumentNotFound)
	}
	return q, nil
}

// Provider returns a MarketProvider whose QuoteFn reads this board.
func (b *QuoteBoard) Provider() *MarketProvider {
	return &MarketProvider{QuoteFn: b.Quote}
}
