package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/models"
)

// Portfolio history and risk analytics (story NW-10). Shapes: api/openapi.yaml#/components/schemas/HistoryRange,
// EquityPoint, EquityHistory, AllocationSlice and PortfolioAnalytics. Snapshots are money (decimal, 2 decimals);
// the risk metrics are statistics over float64 (internal/analytics) rounded half-up to 2 decimals in the DTO.

// StartingEquity is the virtual capital every account starts with; total_return_pct is measured from it.
var StartingEquity = decimal.NewFromInt(100000)

// CashLabel is the AllocationSlice.Label of the cash slice.
const CashLabel = "Cash"

// Defaults for SNAPSHOT_INTERVAL and RISK_FREE_RATE.
const (
	DefaultSnapshotInterval = time.Hour
	DefaultRiskFreeRate     = 0.02
)

// HistoryRange is the window of GET /portfolio/history, ending now.
type HistoryRange string

const (
	HistoryRange1D  HistoryRange = "1d"
	HistoryRange1W  HistoryRange = "1w"
	HistoryRange1M  HistoryRange = "1m"
	HistoryRangeAll HistoryRange = "all"
)

// DefaultHistoryRange is used when the request has no range.
const DefaultHistoryRange = HistoryRange1D

// ParseHistoryRange returns the range for s, or a *ValidationError when s is not one of 1d, 1w, 1m, all.
func ParseHistoryRange(s string) (HistoryRange, error) {
	switch r := HistoryRange(s); r {
	case HistoryRange1D, HistoryRange1W, HistoryRange1M, HistoryRangeAll:
		return r, nil
	}
	return "", &ValidationError{Msg: "range must be one of 1d, 1w, 1m, all"}
}

// Window is how far back the range reaches from now; 0 means no lower bound (all).
func (r HistoryRange) Window() time.Duration {
	switch r {
	case HistoryRange1D:
		return 24 * time.Hour
	case HistoryRange1W:
		return 7 * 24 * time.Hour
	case HistoryRange1M:
		return 30 * 24 * time.Hour
	}
	return 0
}

// Bucket is the downsampling width of the range: 0 (raw points) for 1d, an hour for 1w and 1m, a day for all.
// The last snapshot of each UTC bucket is kept.
func (r HistoryRange) Bucket() time.Duration {
	switch r {
	case HistoryRange1W, HistoryRange1M:
		return time.Hour
	case HistoryRangeAll:
		return 24 * time.Hour
	}
	return 0
}

// EquityPoint is one point of the history (#/components/schemas/EquityPoint). T is UTC.
type EquityPoint struct {
	T      time.Time `json:"t"`
	Equity float64   `json:"equity"`
}

// EquityHistory is the body of GET /portfolio/history (#/components/schemas/EquityHistory). Points is oldest
// first and never nil.
type EquityHistory struct {
	Range  HistoryRange  `json:"range"`
	Points []EquityPoint `json:"points"`
}

// AllocationSlice is one slice of PortfolioAnalytics.Allocation (#/components/schemas/AllocationSlice).
// OrderbookID is nil for the cash slice.
type AllocationSlice struct {
	Label       string  `json:"label"`
	OrderbookID *string `json:"orderbook_id"`
	Value       float64 `json:"value"`
	Weight      float64 `json:"weight"`
}

// PortfolioAnalytics is the body of GET /portfolio/analytics (#/components/schemas/PortfolioAnalytics).
// The pointer metrics are nil (JSON null) without SufficientData; Allocation is never nil.
type PortfolioAnalytics struct {
	TotalReturnPct          float64           `json:"total_return_pct"`
	VolatilityAnnualizedPct *float64          `json:"volatility_annualized_pct"`
	SharpeRatio             *float64          `json:"sharpe_ratio"`
	MaxDrawdownPct          *float64          `json:"max_drawdown_pct"`
	SufficientData          bool              `json:"sufficient_data"`
	DataPoints              int               `json:"data_points"`
	Allocation              []AllocationSlice `json:"allocation"`
}

// SnapshotRepository stores equity snapshots. Implementation: internal/repository/snapshot.go.
type SnapshotRepository interface {
	// InsertBatch writes the snapshots in as few INSERTs as possible (batches); nothing for an empty slice.
	InsertBatch(ctx context.Context, snapshots []models.EquitySnapshot) error

	// ListByUser returns the user's snapshots with t >= since (every snapshot when since is zero), oldest
	// first (t asc, then id asc). It returns an empty, non-nil slice when there are none.
	ListByUser(ctx context.Context, userID uint, since time.Time) ([]models.EquitySnapshot, error)

	// UserIDsWithOrders returns every non-deleted user with at least one order, ascending: the users the
	// periodic snapshot covers.
	UserIDsWithOrders(ctx context.Context) ([]uint, error)
}

// FillObserver is told about every fill that changed a user's cash or positions, after it is committed.
// The snapshot service implements it to snapshot after each fill; implementations must not fail the fill.
type FillObserver interface {
	OrderFilled(ctx context.Context, userID uint)
}
