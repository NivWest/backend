package services

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"backend/internal/domain"
)

// Live quote fan-out (story NW-09). One poller goroutine per instrument with at least one subscriber polls the
// provider every interval and pushes changed quotes to every subscriber of that instrument, so N clients
// watching one instrument cost one upstream poll. Subscriptions are refcounted: the poller starts with the first
// subscriber and stops when the last one leaves.

var _ domain.QuoteHub = (*QuoteHub)(nil)

// DefaultQuoteHubInterval is the poll interval when none is configured (MARKET_STREAM_INTERVAL).
const DefaultQuoteHubInterval = 2 * time.Second

// maxParallelSubscribeQuotes bounds the upstream calls Subscribe makes at once to resolve new ids.
const maxParallelSubscribeQuotes = 8

// maxSubscribeAttempts bounds how often Subscribe re-resolves ids whose topic vanished while it was resolving.
const maxSubscribeAttempts = 3

// Ticker is the part of *time.Ticker the hub uses, so tests can drive the pollers by hand.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// NewRealTicker wraps time.NewTicker; it is the default QuoteHubOptions.NewTicker.
func NewRealTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

// QuoteHubOptions configures a QuoteHub. Zero values pick the defaults.
type QuoteHubOptions struct {
	// Interval between two polls of one instrument (default DefaultQuoteHubInterval).
	Interval time.Duration
	// NewTicker creates each poller's ticker (default NewRealTicker).
	NewTicker func(d time.Duration) Ticker
	// Logf logs poll failures (default log.Printf).
	Logf func(format string, args ...any)
}

// QuoteHub implements domain.QuoteHub on top of a domain.MarketDataProvider. It is safe for concurrent use.
type QuoteHub struct {
	provider  domain.MarketDataProvider
	interval  time.Duration
	newTicker func(time.Duration) Ticker
	logf      func(string, ...any)

	mu     sync.Mutex
	topics map[string]*quoteTopic
	subs   map[*quoteSubscriber]struct{}
	closed bool
	done   chan struct{} // closed by Close
	wg     sync.WaitGroup
}

// quoteTopic is one polled instrument and its subscribers. Guarded by QuoteHub.mu.
type quoteTopic struct {
	id      string
	subs    map[*quoteSubscriber]struct{}
	last    domain.Quote
	failing bool // the last poll failed (logged once per failure streak)
	ctx     context.Context
	cancel  context.CancelFunc
}

// quoteSubscriber is one Subscribe call. Its buffer holds one slot per subscribed instrument, so after coalescing
// (only the latest quote per instrument) a delivery always fits and never blocks the poller.
type quoteSubscriber struct {
	ids []string

	mu     sync.Mutex
	ch     chan domain.Quote
	closed bool
}

// NewQuoteHub returns a hub polling provider. Call Close on shutdown to end every subscription.
func NewQuoteHub(provider domain.MarketDataProvider, opts QuoteHubOptions) *QuoteHub {
	if opts.Interval <= 0 {
		opts.Interval = DefaultQuoteHubInterval
	}
	if opts.NewTicker == nil {
		opts.NewTicker = NewRealTicker
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	return &QuoteHub{
		provider:  provider,
		interval:  opts.Interval,
		newTicker: opts.NewTicker,
		logf:      opts.Logf,
		topics:    map[string]*quoteTopic{},
		subs:      map[*quoteSubscriber]struct{}{},
		done:      make(chan struct{}),
	}
}

var errHubClosed = fmt.Errorf("quote hub is shutting down: %w", domain.ErrMarketDataUnavailable)

// Subscribe implements domain.QuoteHub. ids must be non-empty, distinct and non-blank (the handler validates them).
func (h *QuoteHub) Subscribe(ctx context.Context, ids []string) (<-chan domain.Quote, error) {
	if len(ids) == 0 {
		return nil, &domain.ValidationError{Msg: "ids must list at least one orderbook id"}
	}

	// Resolve the ids no poller knows yet: this rejects unknown ids before anything is subscribed and gives a
	// brand-new topic its first quote, so the subscriber gets every quote immediately. The upstream calls run
	// without the lock; if a topic disappears meanwhile (its last subscriber left), its id is resolved again.
	fetched := map[string]domain.Quote{}
	for attempt := 0; ; attempt++ {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, errHubClosed
		}
		var missing []string
		for _, id := range ids {
			if _, ok := h.topics[id]; !ok {
				if _, ok := fetched[id]; !ok {
					missing = append(missing, id)
				}
			}
		}
		if len(missing) == 0 {
			break // h.mu stays locked for the registration below
		}
		h.mu.Unlock()
		if attempt >= maxSubscribeAttempts {
			return nil, fmt.Errorf("quote hub: subscriptions kept changing: %w", domain.ErrMarketDataUnavailable)
		}
		quotes, err := h.fetchQuotes(ctx, missing)
		if err != nil {
			return nil, err
		}
		for id, q := range quotes {
			fetched[id] = q
		}
	}
	defer h.mu.Unlock()

	sub := &quoteSubscriber{ids: append([]string(nil), ids...), ch: make(chan domain.Quote, len(ids))}
	for _, id := range ids {
		topic, ok := h.topics[id]
		if !ok {
			topic = h.startTopicLocked(id, fetched[id])
		}
		topic.subs[sub] = struct{}{}
		sub.deliver(topic.last)
	}
	h.subs[sub] = struct{}{}

	go func() {
		select {
		case <-ctx.Done():
			h.unsubscribe(sub)
		case <-h.done:
		}
	}()
	return sub.ch, nil
}

// fetchQuotes gets the quote of every id, at most maxParallelSubscribeQuotes at a time. On failure it returns
// the error of the first failing id in ids order (an unknown id wins over a later upstream failure).
func (h *QuoteHub) fetchQuotes(ctx context.Context, ids []string) (map[string]domain.Quote, error) {
	quotes := make([]domain.Quote, len(ids))
	errs := make([]error, len(ids))
	sem := make(chan struct{}, maxParallelSubscribeQuotes)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			quotes[i], errs[i] = h.provider.Quote(ctx, id)
		}()
	}
	wg.Wait()
	out := make(map[string]domain.Quote, len(ids))
	for i, id := range ids {
		if errs[i] != nil {
			return nil, errs[i]
		}
		out[id] = quotes[i]
	}
	return out, nil
}

// startTopicLocked registers a topic seeded with q and starts its poller. Caller holds h.mu.
func (h *QuoteHub) startTopicLocked(id string, q domain.Quote) *quoteTopic {
	ctx, cancel := context.WithCancel(context.Background())
	topic := &quoteTopic{id: id, subs: map[*quoteSubscriber]struct{}{}, last: q, ctx: ctx, cancel: cancel}
	h.topics[id] = topic
	ticker := h.newTicker(h.interval)
	h.wg.Add(1)
	go h.poll(topic, ticker)
	return topic
}

// poll runs one instrument's poller until its topic is stopped.
func (h *QuoteHub) poll(topic *quoteTopic, ticker Ticker) {
	defer h.wg.Done()
	defer ticker.Stop()
	for {
		select {
		case <-topic.ctx.Done():
			return
		case <-ticker.C():
		}
		q, err := h.provider.Quote(topic.ctx, topic.id)
		if topic.ctx.Err() != nil {
			return
		}
		h.publish(topic, q, err)
	}
}

// publish records one poll result and pushes a changed quote to the topic's subscribers.
func (h *QuoteHub) publish(topic *quoteTopic, q domain.Quote, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.topics[topic.id] != topic {
		return // stopped while polling
	}
	if err != nil {
		if !topic.failing {
			h.logf("[STREAM] quote poll for %s failed (keeping the last quote): %v", topic.id, err)
		}
		topic.failing = true
		return
	}
	if topic.failing {
		h.logf("[STREAM] quote poll for %s recovered", topic.id)
		topic.failing = false
	}
	if sameQuote(topic.last, q) {
		return
	}
	topic.last = q
	for sub := range topic.subs {
		sub.deliver(q)
	}
}

// sameQuote reports whether b carries no new price information over a (updated_at alone is not a change).
func sameQuote(a, b domain.Quote) bool {
	return a.OrderbookID == b.OrderbookID && a.Last == b.Last && a.Bid == b.Bid && a.Ask == b.Ask &&
		a.ChangePercent == b.ChangePercent
}

func (h *QuoteHub) unsubscribe(sub *quoteSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropSubscriberLocked(sub)
}

// dropSubscriberLocked removes sub from its topics, stops topics nobody watches any more and closes sub's
// channel. Caller holds h.mu.
func (h *QuoteHub) dropSubscriberLocked(sub *quoteSubscriber) {
	for _, id := range sub.ids {
		topic, ok := h.topics[id]
		if !ok {
			continue
		}
		delete(topic.subs, sub)
		if len(topic.subs) == 0 {
			topic.cancel()
			delete(h.topics, id)
		}
	}
	delete(h.subs, sub)
	sub.close()
}

// Close ends every subscription (their channels close), stops every poller and waits for them. Later Subscribe
// calls fail with an error wrapping domain.ErrMarketDataUnavailable. Safe to call more than once.
func (h *QuoteHub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	close(h.done)
	for id, topic := range h.topics {
		topic.cancel()
		delete(h.topics, id)
	}
	for sub := range h.subs {
		sub.close()
		delete(h.subs, sub)
	}
	h.mu.Unlock()
	h.wg.Wait()
}

// Pollers returns how many instruments are being polled right now (one poller each).
func (h *QuoteHub) Pollers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.topics)
}

// deliver queues q without blocking. When the buffer is full the subscriber is slow: queued quotes of the same
// instrument are dropped (oldest first) so only the latest quote of each instrument stays queued.
func (s *quoteSubscriber) deliver(q domain.Quote) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- q:
		return
	default:
	}
	queued := make([]domain.Quote, 0, cap(s.ch)+1)
drain:
	for {
		select {
		case old := <-s.ch:
			queued = append(queued, old)
		default:
			break drain
		}
	}
	queued = append(queued, q)
	for i, old := range queued {
		if supersededAfter(queued, i, old.OrderbookID) {
			continue
		}
		select {
		case s.ch <- old:
		default:
			// Cannot happen: at most one quote per subscribed instrument is left and the buffer has a slot for each.
		}
	}
}

// supersededAfter reports whether a quote of id follows position i in qs.
func supersededAfter(qs []domain.Quote, i int, id string) bool {
	for _, q := range qs[i+1:] {
		if q.OrderbookID == id {
			return true
		}
	}
	return false
}

func (s *quoteSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}
