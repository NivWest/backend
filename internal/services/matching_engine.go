package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

// DefaultMatchingWorkers bounds how many instruments the engine quotes and matches at once.
const DefaultMatchingWorkers = 8

// MatchingEngine fills OPEN LIMIT and STOP orders when the market reaches their price (story NW-08).
//
// Each cycle: only the replica holding the LeaderLock goes on; every OPEN order is read and grouped by
// instrument; each instrument is quoted once, by a bounded pool of workers; each triggered order is priced
// with PriceOrder (slippage and commission like a market order) and filled in its own DB transaction. A fill
// without enough buying power left is REJECTED with a reason, which releases its reservation. An order that
// left OPEN meanwhile (a cancel won the race) is skipped.
type MatchingEngine struct {
	repo    domain.MatchingRepository
	quotes  domain.QuoteSource
	lock    domain.LeaderLock
	now     func() time.Time
	workers int
}

// NewMatchingEngine returns an engine over repo, quotes and lock. A nil now defaults to time.Now; workers < 1
// defaults to DefaultMatchingWorkers.
func NewMatchingEngine(repo domain.MatchingRepository, quotes domain.QuoteSource, lock domain.LeaderLock, now func() time.Time, workers int) *MatchingEngine {
	if now == nil {
		now = time.Now
	}
	if workers < 1 {
		workers = DefaultMatchingWorkers
	}
	return &MatchingEngine{repo: repo, quotes: quotes, lock: lock, now: now, workers: workers}
}

// CycleResult summarizes one cycle. Skipped is true when this replica does not hold the leader lock.
type CycleResult struct {
	Skipped  bool
	Open     int
	Filled   int
	Rejected int
	// Errors counts instruments that could not be quoted and orders whose fill or reject failed unexpectedly;
	// they are retried on the next cycle.
	Errors int
}

// Triggered reports whether an open order executes against bid and ask:
//
//   - BUY LIMIT when ask ≤ limit_price; SELL LIMIT when bid ≥ limit_price;
//   - SELL STOP when bid ≤ stop_price; BUY STOP when ask ≥ stop_price.
//
// An order without the price its type needs never triggers.
func Triggered(o domain.OpenOrder, bid, ask decimal.Decimal) bool {
	buy := o.Side == domain.OrderSideBuy
	switch o.Type {
	case domain.OrderTypeLimit:
		if o.LimitPrice == nil {
			return false
		}
		if buy {
			return ask.LessThanOrEqual(*o.LimitPrice)
		}
		return bid.GreaterThanOrEqual(*o.LimitPrice)
	case domain.OrderTypeStop:
		if o.StopPrice == nil {
			return false
		}
		if buy {
			return ask.GreaterThanOrEqual(*o.StopPrice)
		}
		return bid.LessThanOrEqual(*o.StopPrice)
	default:
		return false
	}
}

// Run releases the leader lock when ctx is done and runs a cycle on every tick of interval until then. It
// blocks, so start it in its own goroutine.
func (e *MatchingEngine) Run(ctx context.Context, interval time.Duration) {
	defer func() {
		if err := e.lock.Release(); err != nil {
			log.Printf("[MATCHING] release leader lock: %v", err)
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	leader := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			res, err := e.RunCycle(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("[MATCHING] cycle failed: %v", err)
				}
				continue
			}
			if !res.Skipped != leader {
				leader = !res.Skipped
				log.Printf("[MATCHING] leader: %v", leader)
			}
			if res.Filled > 0 || res.Rejected > 0 || res.Errors > 0 {
				log.Printf("[MATCHING] cycle: %d open, %d filled, %d rejected, %d errors", res.Open, res.Filled, res.Rejected, res.Errors)
			}
		}
	}
}

// RunCycle runs one matching cycle (see MatchingEngine). It returns an error only when the lock or the order
// book cannot be read; per-instrument and per-order failures are counted in CycleResult.Errors.
func (e *MatchingEngine) RunCycle(ctx context.Context) (CycleResult, error) {
	held, err := e.lock.TryAcquire(ctx)
	if err != nil {
		return CycleResult{Skipped: true}, fmt.Errorf("leader lock: %w", err)
	}
	if !held {
		return CycleResult{Skipped: true}, nil
	}

	open, err := e.repo.ListOpen(ctx)
	if err != nil {
		return CycleResult{}, fmt.Errorf("list open orders: %w", err)
	}
	result := CycleResult{Open: len(open)}
	if len(open) == 0 {
		return result, nil
	}

	// Group by instrument, in a stable order (oldest order first within an instrument).
	byInstrument := map[string][]domain.OpenOrder{}
	for _, o := range open {
		byInstrument[o.OrderbookID] = append(byInstrument[o.OrderbookID], o)
	}
	instruments := make([]string, 0, len(byInstrument))
	for id := range byInstrument {
		instruments = append(instruments, id)
	}
	sort.Strings(instruments)

	jobs := make(chan string)
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for range min(e.workers, len(instruments)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				r := e.matchInstrument(ctx, id, byInstrument[id])
				mu.Lock()
				result.Filled += r.Filled
				result.Rejected += r.Rejected
				result.Errors += r.Errors
				mu.Unlock()
			}
		}()
	}
	for _, id := range instruments {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	return result, nil
}

// matchInstrument quotes one instrument and fills or rejects its triggered orders, one transaction each.
func (e *MatchingEngine) matchInstrument(ctx context.Context, orderbookID string, orders []domain.OpenOrder) CycleResult {
	var r CycleResult
	quote, err := e.quotes.Quote(ctx, orderbookID)
	if err != nil || quote.Bid <= 0 || quote.Ask <= 0 {
		if err == nil {
			err = fmt.Errorf("no valid bid/ask (bid %v, ask %v)", quote.Bid, quote.Ask)
		}
		log.Printf("[MATCHING] quote %s: %v", orderbookID, err)
		r.Errors++
		return r
	}
	bid, ask := decimal.NewFromFloat(quote.Bid), decimal.NewFromFloat(quote.Ask)

	for _, o := range orders {
		if ctx.Err() != nil {
			return r
		}
		if !Triggered(o, bid, ask) {
			continue
		}
		pricing := PriceOrder(o.Side, o.Quantity, bid, ask)
		_, err := e.repo.FillOrder(ctx, domain.FillOrderParams{
			OrderID:    o.ID,
			FillPrice:  pricing.FillPrice,
			Commission: pricing.Commission,
			Total:      pricing.Total,
			FilledAt:   e.now().UTC().Truncate(time.Microsecond),
		})
		switch {
		case err == nil:
			r.Filled++
		case errors.Is(err, domain.ErrOrderNotOpen), errors.Is(err, domain.ErrOrderNotFound):
			// Cancelled (or purged) since ListOpen: the other transaction won.
		case errors.Is(err, domain.ErrInsufficientFunds), errors.Is(err, domain.ErrInsufficientShares):
			_, rejErr := e.repo.RejectOrder(ctx, o.ID, rejectReason(err, pricing))
			switch {
			case rejErr == nil:
				r.Rejected++
			case errors.Is(rejErr, domain.ErrOrderNotOpen), errors.Is(rejErr, domain.ErrOrderNotFound):
				// Cancelled between the failed fill and the reject.
			default:
				log.Printf("[MATCHING] reject order %d: %v", o.ID, rejErr)
				r.Errors++
			}
		default:
			log.Printf("[MATCHING] fill order %d: %v", o.ID, err)
			r.Errors++
		}
	}
	return r
}

// rejectReason is the reject_reason stored on an order the engine could not fill.
func rejectReason(err error, pricing OrderPricing) string {
	if errors.Is(err, domain.ErrInsufficientShares) {
		return "insufficient shares at fill time"
	}
	return fmt.Sprintf("insufficient funds at fill time: total %s SEK exceeds buying power", pricing.Total.StringFixed(2))
}
