package domain

import "context"

type StockProvider interface {
	Search(ctx context.Context, query string) ([]byte, error)
	GetPriceChart(ctx context.Context, orderbookID, timePeriod string) ([]byte, error)
	GetStockDetails(ctx context.Context, orderbookID string) ([]byte, error)
	GetStockQuote(ctx context.Context, orderbookID string) ([]byte, error)
}