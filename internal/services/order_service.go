package services

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"time"

	"backend/internal/domain"
)

type OrderService struct {
	repo          domain.OrderRepository
	stockProvider domain.StockProvider
}

func NewOrderService(repo domain.OrderRepository, stockProvider domain.StockProvider) *OrderService {
	return &OrderService{
		repo:          repo,
		stockProvider: stockProvider,
	}
}

func (s *OrderService) ExecuteOrder(ctx context.Context, userID uint, req domain.OrderRequest) error {
	// 1. Friction Logic: Artificial Latency (50-500ms)
	latency := time.Duration(rand.Intn(450)+50) * time.Millisecond
	time.Sleep(latency)

	// 2. Fetch real-time quote
	quoteBytes, err := s.stockProvider.GetStockQuote(ctx, req.Symbol)
	if err != nil {
		return errors.New("failed to fetch live stock quote for execution")
	}

	var quote avanzaQuote // Reuse the unexported struct from portfolio_service or define it here
	if err := json.Unmarshal(quoteBytes, &quote); err != nil {
		return errors.New("failed to parse live stock quote")
	}

	if quote.LastPrice <= 0 {
		return errors.New("invalid stock price received")
	}

	price := quote.LastPrice

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
