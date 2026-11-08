package services

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/analytics"
	"backend/internal/domain"
	"backend/internal/models"
)

// PortfolioReader is what AnalyticsService needs from the live portfolio (PortfolioService implements it).
type PortfolioReader interface {
	GetPortfolio(ctx context.Context, userID uint) (*domain.Portfolio, error)
}

// AnalyticsService serves the equity history and the risk analytics of a user (story NW-10) from the stored
// equity snapshots and the live portfolio. The math lives in internal/analytics; this service picks the data,
// converts to percent and rounds half-up to 2 decimals.
type AnalyticsService struct {
	snapshots domain.SnapshotRepository
	portfolio PortfolioReader
	riskFree  float64
	now       func() time.Time
}

// NewAnalyticsService returns an AnalyticsService. riskFree is the annual risk-free rate as a fraction
// (RISK_FREE_RATE, default 0.02); a nil now defaults to time.Now.
func NewAnalyticsService(
	snapshots domain.SnapshotRepository,
	portfolio PortfolioReader,
	riskFree float64,
	now func() time.Time,
) *AnalyticsService {
	if now == nil {
		now = time.Now
	}
	return &AnalyticsService{snapshots: snapshots, portfolio: portfolio, riskFree: riskFree, now: now}
}

// GetHistory returns userID's equity points in the range rawRange ("" means domain.DefaultHistoryRange),
// oldest first: raw for 1d, the last snapshot of each UTC hour for 1w and 1m, of each UTC day for all.
//
// Errors: *domain.ValidationError for an unknown range, otherwise whatever the repository returns.
func (s *AnalyticsService) GetHistory(ctx context.Context, userID uint, rawRange string) (*domain.EquityHistory, error) {
	rng := domain.DefaultHistoryRange
	if rawRange != "" {
		r, err := domain.ParseHistoryRange(rawRange)
		if err != nil {
			return nil, err
		}
		rng = r
	}

	var since time.Time
	if w := rng.Window(); w > 0 {
		since = s.now().UTC().Add(-w)
	}
	snaps, err := s.snapshots.ListByUser(ctx, userID, since)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	kept := downsample(snaps, rng.Bucket())
	history := &domain.EquityHistory{Range: rng, Points: make([]domain.EquityPoint, 0, len(kept))}
	for _, snap := range kept {
		history.Points = append(history.Points, domain.EquityPoint{T: snap.T.UTC(), Equity: domain.MoneyFloat(snap.Equity)})
	}
	return history, nil
}

// downsample keeps the last snapshot of every bucket (UTC-aligned, since time.Truncate counts from the zero
// time); bucket 0 keeps every snapshot. snaps must be oldest first.
func downsample(snaps []models.EquitySnapshot, bucket time.Duration) []models.EquitySnapshot {
	if bucket <= 0 {
		return snaps
	}
	out := make([]models.EquitySnapshot, 0, len(snaps))
	for _, snap := range snaps {
		key := snap.T.UTC().Truncate(bucket)
		if n := len(out); n > 0 && out[n-1].T.UTC().Truncate(bucket).Equal(key) {
			out[n-1] = snap
			continue
		}
		out = append(out, snap)
	}
	return out
}

// GetAnalytics returns userID's risk-adjusted performance (api/openapi.yaml#/components/schemas/PortfolioAnalytics):
// total return of the live total equity from domain.StartingEquity; volatility, Sharpe and max drawdown over the
// daily series of all snapshots once it has analytics.MinDailyReturns daily returns (null before); and the
// allocation of the live total equity.
func (s *AnalyticsService) GetAnalytics(ctx context.Context, userID uint) (*domain.PortfolioAnalytics, error) {
	portfolio, err := s.portfolio.GetPortfolio(ctx, userID)
	if err != nil {
		return nil, err
	}
	snaps, err := s.snapshots.ListByUser(ctx, userID, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	points := make([]analytics.Point, len(snaps))
	for i, snap := range snaps {
		points[i] = analytics.Point{T: snap.T, Equity: snap.Equity.InexactFloat64()}
	}
	daily := analytics.Equities(analytics.DailySeries(points))
	returns := analytics.DailyReturns(daily)

	out := &domain.PortfolioAnalytics{
		TotalReturnPct: percent(analytics.TotalReturn(domain.StartingEquity.InexactFloat64(), portfolio.TotalEquity)),
		DataPoints:     len(returns),
		SufficientData: len(returns) >= analytics.MinDailyReturns,
		Allocation:     allocation(portfolio),
	}
	if out.SufficientData {
		if vol, ok := analytics.AnnualizedVolatility(returns); ok {
			out.VolatilityAnnualizedPct = ptr(percent(vol))
		}
		if sharpe, ok := analytics.Sharpe(returns, s.riskFree); ok {
			out.SharpeRatio = ptr(round2(sharpe))
		}
		out.MaxDrawdownPct = ptr(percent(analytics.MaxDrawdown(daily)))
	}
	return out, nil
}

// allocation splits the portfolio's equity into one slice per position (label = symbol, largest first as in
// Portfolio.Positions) and a final cash slice. Weights are value / total × 100 rounded half-up to 2 decimals;
// the rounding remainder goes to the largest slice so the weights sum to exactly 100 (all 0 when total is 0).
func allocation(p *domain.Portfolio) []domain.AllocationSlice {
	values := make([]decimal.Decimal, 0, len(p.Positions)+1)
	slices := make([]domain.AllocationSlice, 0, len(p.Positions)+1)
	for _, pos := range p.Positions {
		id := pos.OrderbookID
		v := decimal.NewFromFloat(pos.MarketValue)
		values = append(values, v)
		slices = append(slices, domain.AllocationSlice{Label: pos.Symbol, OrderbookID: &id, Value: domain.MoneyFloat(v)})
	}
	cash := decimal.NewFromFloat(p.Cash)
	values = append(values, cash)
	slices = append(slices, domain.AllocationSlice{Label: domain.CashLabel, Value: domain.MoneyFloat(cash)})
	if p.VaultValue > 0 {
		vault := decimal.NewFromFloat(p.VaultValue)
		values = append(values, vault)
		slices = append(slices, domain.AllocationSlice{Label: domain.VaultLabel, Value: domain.MoneyFloat(vault)})
	}

	total := decimal.Zero
	for _, v := range values {
		total = total.Add(v)
	}
	if !total.IsPositive() {
		return slices
	}

	sum := decimal.Zero
	largest := 0
	weights := make([]decimal.Decimal, len(values))
	for i, v := range values {
		weights[i] = v.Div(total).Mul(hundred).Round(2)
		sum = sum.Add(weights[i])
		if v.GreaterThan(values[largest]) {
			largest = i
		}
	}
	weights[largest] = weights[largest].Add(hundred.Sub(sum))
	for i := range slices {
		slices[i].Weight = weights[i].InexactFloat64()
	}
	return slices
}

// percent converts a fraction to percent rounded half-up to 2 decimals (0.0123 → 1.23).
func percent(fraction float64) float64 { return round2(fraction * 100) }

// round2 rounds half-up (away from zero) to 2 decimals via the float's shortest decimal representation, so
// 1.005 rounds to 1.01; it never returns −0.
func round2(x float64) float64 { return decimal.NewFromFloat(x).Round(2).InexactFloat64() }

func ptr(f float64) *float64 { return &f }
