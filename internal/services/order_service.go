package services

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"backend/internal/domain"
)

type OrderService struct {
	repo     domain.OrderRepository
	provider domain.MarketDataProvider
	// sleep simulates execution latency. It is a field so tests can replace it.
	sleep func(time.Duration)
}

func NewOrderService(repo domain.OrderRepository, provider domain.MarketDataProvider) *OrderService {
	return &OrderService{
		repo:     repo,
		provider: provider,
		sleep:    time.Sleep,
	}
}

func (s *OrderService) ExecuteOrder(ctx context.Context, userID uint, req domain.OrderRequest) error {
	// 1. Friction Logic: Artificial Latency (50-500ms)
	latency := time.Duration(rand.Intn(450)+50) * time.Millisecond
	s.sleep(latency)

	// 2. Fetch real-time quote. Orders still carry only a symbol, which is passed as the instrument id
	// as before; identifying instruments by orderbook_id in trading is NW-04's job.
	quote, err := s.provider.Quote(ctx, req.Symbol)
	if err != nil {
		return errors.New("failed to fetch live stock quote for execution")
	}

	if quote.Last <= 0 {
		return errors.New("invalid stock price received")
	}

	price := quote.Last

	// 3. Friction Logic: Slippage penalty for high volume
	if req.Quantity > 100 { // Arbitrary high volume threshold
		slippage := price * 0.005 // 0.5% slippage
		if req.Type == "BUY" {
			price += slippage // Buy higher
		} else {
			price -= slippage // Sell lower
		}
	}

	total := price * req.Quantity

	// 4. Ensure stock exists in our DB
	stock, err := s.repo.EnsureStock(ctx, req.Symbol, req.Name)
	if err != nil {
		return errors.New("failed to register stock in database")
	}

	// 5. Execute Database Transaction
	return s.repo.ExecuteTransaction(
		ctx,
		userID,
		stock.ID,
		req.Type,
		req.Quantity,
		price,
		total,
	)
}
