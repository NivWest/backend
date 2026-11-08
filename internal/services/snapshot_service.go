package services

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
	"backend/internal/models"
)

// snapshotWorkers bounds how many users SnapshotAll values at once.
const snapshotWorkers = 8

// fillSnapshotTimeout bounds the snapshot taken after a fill; it runs detached from the request's context so
// a client that hangs up right after its order still gets the snapshot.
const fillSnapshotTimeout = 5 * time.Second

// SnapshotService writes equity snapshots (story NW-10): one after every fill (OrderFilled, a
// domain.FillObserver) and one per user with an order on every scheduler tick (SnapshotAll). Positions are
// valued exactly like GET /portfolio (fetchPositionQuote), so a snapshot taken right after a fill equals the
// portfolio's total_equity.
type SnapshotService struct {
	snapshots domain.SnapshotRepository
	positions domain.PortfolioRepository
	users     domain.UserRepository
	provider  domain.MarketDataProvider
	now       func() time.Time
}

var _ domain.FillObserver = (*SnapshotService)(nil)

// NewSnapshotService returns a SnapshotService; a nil now defaults to time.Now.
func NewSnapshotService(
	snapshots domain.SnapshotRepository,
	positions domain.PortfolioRepository,
	users domain.UserRepository,
	provider domain.MarketDataProvider,
	now func() time.Time,
) *SnapshotService {
	if now == nil {
		now = time.Now
	}
	return &SnapshotService{snapshots: snapshots, positions: positions, users: users, provider: provider, now: now}
}

// quoteFunc returns the valuation quote of one instrument.
type quoteFunc func(ctx context.Context, orderbookID string) positionQuote

// TakeSnapshot values userID now with live quotes and stores the snapshot.
func (s *SnapshotService) TakeSnapshot(ctx context.Context, userID uint) error {
	snap, err := s.value(ctx, userID, s.now().UTC(), s.liveQuote)
	if err != nil {
		return err
	}
	if err := s.snapshots.InsertBatch(ctx, []models.EquitySnapshot{snap}); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}
	return nil
}

// OrderFilled implements domain.FillObserver: it snapshots the user after a fill. A failure is logged and never
// reaches the order (the fill is already committed).
func (s *SnapshotService) OrderFilled(ctx context.Context, userID uint) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fillSnapshotTimeout)
	defer cancel()
	if err := s.TakeSnapshot(ctx, userID); err != nil {
		log.Printf("[SNAPSHOT] after fill failed for user_id=%d: %v", userID, err)
	}
}

// SnapshotAll snapshots every user with an order at one shared instant, quoting each instrument at most once,
// and stores the snapshots in batches. A user that cannot be valued is logged and skipped; it returns how many
// snapshots were written.
func (s *SnapshotService) SnapshotAll(ctx context.Context) (int, error) {
	ids, err := s.snapshots.UserIDsWithOrders(ctx)
	if err != nil {
		return 0, fmt.Errorf("list users with orders: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	at := s.now().UTC()
	quote := s.cachedQuotes()
	results := make([]*models.EquitySnapshot, len(ids))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(snapshotWorkers, len(ids)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				snap, err := s.value(ctx, ids[i], at, quote)
				if err != nil {
					log.Printf("[SNAPSHOT] periodic snapshot failed for user_id=%d: %v", ids[i], err)
					continue
				}
				results[i] = &snap // each worker writes only its own index
			}
		}()
	}
	for i := range ids {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	batch := make([]models.EquitySnapshot, 0, len(ids))
	for _, r := range results {
		if r != nil {
			batch = append(batch, *r)
		}
	}
	if err := s.snapshots.InsertBatch(ctx, batch); err != nil {
		return 0, fmt.Errorf("insert snapshots: %w", err)
	}
	return len(batch), nil
}

// value computes userID's snapshot at t: cash, holdings at the quote's last price (avg_cost when the quote
// fails), vault 0 (until NW-11) and equity = round2(cash + holdings + vault), the same rounding as
// Portfolio.TotalEquity.
func (s *SnapshotService) value(ctx context.Context, userID uint, t time.Time, quote quoteFunc) (models.EquitySnapshot, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return models.EquitySnapshot{}, fmt.Errorf("load user: %w", err)
	}
	positions, err := s.positions.GetUserPositions(ctx, userID)
	if err != nil {
		return models.EquitySnapshot{}, fmt.Errorf("load positions: %w", err)
	}

	holdings := decimal.Zero
	for _, pos := range positions {
		price := pos.AvgCost
		if q := quote(ctx, pos.Stock.OrderbookID); q.ok {
			price = q.last
		}
		holdings = holdings.Add(price.Mul(pos.Quantity))
	}
	vault := decimal.Zero
	return models.EquitySnapshot{
		UserID:        userID,
		T:             t,
		Cash:          user.Balance.Round(2),
		HoldingsValue: holdings.Round(2),
		VaultValue:    vault,
		Equity:        user.Balance.Add(holdings).Add(vault).Round(2),
	}, nil
}

func (s *SnapshotService) liveQuote(ctx context.Context, orderbookID string) positionQuote {
	return fetchPositionQuote(ctx, s.provider, orderbookID)
}

// cachedQuotes returns a quoteFunc that quotes each orderbook id once, also under concurrent callers.
func (s *SnapshotService) cachedQuotes() quoteFunc {
	type entry struct {
		once sync.Once
		q    positionQuote
	}
	var mu sync.Mutex
	cache := map[string]*entry{}
	return func(ctx context.Context, orderbookID string) positionQuote {
		mu.Lock()
		e, ok := cache[orderbookID]
		if !ok {
			e = &entry{}
			cache[orderbookID] = e
		}
		mu.Unlock()
		e.once.Do(func() { e.q = fetchPositionQuote(ctx, s.provider, orderbookID) })
		return e.q
	}
}
