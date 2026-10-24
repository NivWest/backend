package services

import (
	"context"
	"strings"

	"backend/internal/domain"
)

// MarketService validates market data requests and delegates them to the configured provider
// (Avanza or the simulated market). Provider errors pass through unchanged so handlers can map
// domain.ErrInstrumentNotFound and domain.ErrMarketDataUnavailable.
type MarketService struct {
	provider domain.MarketDataProvider
}

func NewMarketService(provider domain.MarketDataProvider) *MarketService {
	return &MarketService{provider: provider}
}

// Search returns the instruments matching q (trimmed). A blank q is a *domain.ValidationError.
// The result is never nil.
func (s *MarketService) Search(ctx context.Context, q string) ([]domain.InstrumentSummary, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, &domain.ValidationError{Msg: "q must not be blank"}
	}
	items, err := s.provider.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []domain.InstrumentSummary{}
	}
	return items, nil
}

// Quote returns the latest quote of one instrument.
func (s *MarketService) Quote(ctx context.Context, id string) (domain.Quote, error) {
	id, err := validateOrderbookID(id)
	if err != nil {
		return domain.Quote{}, err
	}
	return s.provider.Quote(ctx, id)
}

// Details returns the description and key indicators of one instrument.
func (s *MarketService) Details(ctx context.Context, id string) (domain.InstrumentDetails, error) {
	id, err := validateOrderbookID(id)
	if err != nil {
		return domain.InstrumentDetails{}, err
	}
	return s.provider.Details(ctx, id)
}

// Candles returns the OHLCV bars of one instrument for period, together with the parsed period.
// An unknown period is a *domain.ValidationError. The candles are never nil.
func (s *MarketService) Candles(ctx context.Context, id, period string) ([]domain.Candle, domain.CandlePeriod, error) {
	id, err := validateOrderbookID(id)
	if err != nil {
		return nil, "", err
	}
	p, err := domain.ParseCandlePeriod(period)
	if err != nil {
		return nil, "", err
	}
	candles, err := s.provider.Candles(ctx, id, p)
	if err != nil {
		return nil, "", err
	}
	if candles == nil {
		candles = []domain.Candle{}
	}
	return candles, p, nil
}

func validateOrderbookID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", &domain.ValidationError{Msg: "orderbook_id must not be blank"}
	}
	return id, nil
}
