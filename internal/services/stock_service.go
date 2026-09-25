package services

import (
	"context"
	"errors"
	"strings"

	"backend/internal/domain"
)

type StockService struct {
	provider domain.StockProvider
}

func NewStockService(provider domain.StockProvider) *StockService {
	return &StockService{
		provider: provider,
	}
}

func (s *StockService) Search(
	ctx context.Context,
	query string,
) ([]byte, error) {
	query = strings.TrimSpace(query)

	if query == "" {
		return nil, errors.New("query cannot be empty")
	}

	return s.provider.Search(ctx, query)
}

func (s *StockService) GetPriceChart(
	ctx context.Context,
	orderbookID string,
	timePeriod string,
) ([]byte, error) {
	if orderbookID == "" {
		return nil, errors.New("orderbook ID cannot be empty")
	}

	return s.provider.GetPriceChart(
		ctx,
		orderbookID,
		timePeriod,
	)
}

func (s *StockService) GetStockDetails(
	ctx context.Context,
	orderbookID string,
) ([]byte, error) {
	if orderbookID == "" {
		return nil, errors.New("orderbook ID cannot be empty")
	}

	return s.provider.GetStockDetails(ctx, orderbookID)
}

func (s *StockService) GetStockQuote(
	ctx context.Context,
	orderbookID string,
) ([]byte, error) {
	if orderbookID == "" {
		return nil, errors.New("orderbook ID cannot be empty")
	}

	return s.provider.GetStockQuote(ctx, orderbookID)
}
