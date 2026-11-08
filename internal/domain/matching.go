package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Matching engine (story NW-08). services.MatchingEngine evaluates every OPEN LIMIT and STOP order against
// the live quote of its instrument once per MATCHING_INTERVAL and fills or rejects it through
// MatchingRepository. Only the replica holding the LeaderLock runs cycles.

// MatchingRepository is the engine's view of the order book. Implementation: internal/repository/matching.go.
// FillOrder and RejectOrder each run in their own DB transaction that locks the user row, the position and
// then the order (FOR UPDATE, the lock order of placement and cancel) and re-checks that the order is still
// OPEN, so a fill racing a cancel serializes: whichever commits first wins, the other gets ErrOrderNotOpen.
type MatchingRepository interface {
	// ListOpen returns every OPEN order, oldest first (by id).
	ListOpen(ctx context.Context) ([]OpenOrder, error)

	// FillOrder executes an OPEN order at a priced fill. It releases the order's reservation and then moves
	// cash and shares like OrderRepository.Execute: a BUY needs balance − (reserved_cash after the release) >=
	// Total (else ErrInsufficientFunds, nothing changes), a SELL credits Total, realizes P&L and deletes the
	// position at zero. The order becomes FILLED with the fill fields and FilledAt. ErrOrderNotOpen when it
	// already left OPEN, ErrOrderNotFound when it does not exist.
	FillOrder(ctx context.Context, p FillOrderParams) (*Order, error)

	// RejectOrder moves an OPEN order to REJECTED with reason and releases its reservation.
	// ErrOrderNotOpen when it already left OPEN, ErrOrderNotFound when it does not exist.
	RejectOrder(ctx context.Context, orderID uint, reason string) (*Order, error)
}

// OpenOrder is an OPEN pending order as the engine evaluates it.
type OpenOrder struct {
	ID          uint
	UserID      uint
	StockID     uint
	OrderbookID string
	Side        OrderSide
	Type        OrderType
	Quantity    decimal.Decimal
	// LimitPrice is set for LIMIT and StopPrice for STOP orders.
	LimitPrice *decimal.Decimal
	StopPrice  *decimal.Decimal
}

// FillOrderParams is a triggered order priced by services.PriceOrder (every amount rounded to 2 decimals).
type FillOrderParams struct {
	OrderID    uint
	FillPrice  decimal.Decimal
	Commission decimal.Decimal
	Total      decimal.Decimal
	FilledAt   time.Time
}

// QuoteSource is the part of MarketDataProvider the engine needs.
type QuoteSource interface {
	Quote(ctx context.Context, orderbookID string) (Quote, error)
}

// LeaderLock elects the one replica that runs the matching engine. Implementation: a Postgres session advisory
// lock (pg_try_advisory_lock) held on a dedicated connection (internal/repository/leader_lock.go).
type LeaderLock interface {
	// TryAcquire returns true while this process holds the lock: it acquires it when free and re-verifies it
	// (the connection is still alive) when already held. It never blocks; false means another replica has it.
	TryAcquire(ctx context.Context) (bool, error)
	// Release gives the lock up (if held) and closes its connection.
	Release() error
}

// MatchingEngineLockKey is the Postgres advisory lock key of the matching engine's leader election
// (0x4E570008, "NW" 08). Every replica of one database competes for the same key.
const MatchingEngineLockKey int64 = 0x4E570008
