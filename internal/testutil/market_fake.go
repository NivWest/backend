package testutil

import (
	"context"

	"backend/internal/domain"
)

var (
	_ domain.MarketDataProvider = (*MarketProvider)(nil)
	_ domain.MarketSimControl   = (*SimControl)(nil)
)

// ---------------------------------------------------------------------------
// MarketDataProvider
// ---------------------------------------------------------------------------

// MarketProvider is a fake domain.MarketDataProvider. Unset functions return ErrNotConfigured.
type MarketProvider struct {
	SearchFn  func(ctx context.Context, query string) ([]domain.InstrumentSummary, error)
	QuoteFn   func(ctx context.Context, orderbookID string) (domain.Quote, error)
	DetailsFn func(ctx context.Context, orderbookID string) (domain.InstrumentDetails, error)
	CandlesFn func(ctx context.Context, orderbookID string, period domain.CandlePeriod) ([]domain.Candle, error)
}

// FixedQuote is a convenience for a MarketProvider QuoteFn that always returns q.
func FixedQuote(q domain.Quote) func(context.Context, string) (domain.Quote, error) {
	return func(context.Context, string) (domain.Quote, error) { return q, nil }
}

func (p *MarketProvider) Search(ctx context.Context, query string) ([]domain.InstrumentSummary, error) {
	if p.SearchFn != nil {
		return p.SearchFn(ctx, query)
	}
	return nil, ErrNotConfigured
}

func (p *MarketProvider) Quote(ctx context.Context, orderbookID string) (domain.Quote, error) {
	if p.QuoteFn != nil {
		return p.QuoteFn(ctx, orderbookID)
	}
	return domain.Quote{}, ErrNotConfigured
}

func (p *MarketProvider) Details(ctx context.Context, orderbookID string) (domain.InstrumentDetails, error) {
	if p.DetailsFn != nil {
		return p.DetailsFn(ctx, orderbookID)
	}
	return domain.InstrumentDetails{}, ErrNotConfigured
}

func (p *MarketProvider) Candles(ctx context.Context, orderbookID string, period domain.CandlePeriod) ([]domain.Candle, error) {
	if p.CandlesFn != nil {
		return p.CandlesFn(ctx, orderbookID, period)
	}
	return nil, ErrNotConfigured
}

// ---------------------------------------------------------------------------
// MarketSimControl
// ---------------------------------------------------------------------------

// SimControl is a fake domain.MarketSimControl. Unset functions succeed.
type SimControl struct {
	SetPriceFn   func(ctx context.Context, orderbookID string, price float64) error
	ClearPriceFn func(ctx context.Context, orderbookID string) error
}

func (s *SimControl) SetPrice(ctx context.Context, orderbookID string, price float64) error {
	if s.SetPriceFn != nil {
		return s.SetPriceFn(ctx, orderbookID, price)
	}
	return nil
}

func (s *SimControl) ClearPrice(ctx context.Context, orderbookID string) error {
	if s.ClearPriceFn != nil {
		return s.ClearPriceFn(ctx, orderbookID)
	}
	return nil
}
