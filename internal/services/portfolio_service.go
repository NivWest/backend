package services

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"backend/internal/domain"
)

type PortfolioService struct {
	repo          domain.PortfolioRepository
	stockProvider domain.StockProvider
	userRepo      domain.UserRepository
}

func NewPortfolioService(
	repo domain.PortfolioRepository,
	stockProvider domain.StockProvider,
	userRepo domain.UserRepository,
) *PortfolioService {
	return &PortfolioService{
		repo:          repo,
		stockProvider: stockProvider,
		userRepo:      userRepo,
	}
}

type DashboardPosition struct {
	StockName     string  `json:"stock_name"`
	Symbol        string  `json:"symbol"`
	Quantity      float64 `json:"quantity"`
	AvgPrice      float64 `json:"avg_price"`
	CurrentPrice  float64 `json:"current_price"`
	ChangePercent float64 `json:"change_percent"`
	Value         float64 `json:"value"`
	Weight        float64 `json:"weight"`
}

type PortfolioDashboard struct {
	Balance          float64             `json:"balance"`
	TotalValue       float64             `json:"total_value"`
	TotalUnrealized  float64             `json:"total_unrealized"`
	Positions        []DashboardPosition `json:"positions"`
	TopMovers        []DashboardPosition `json:"top_movers"`
}

type avanzaQuote struct {
	LastPrice     float64 `json:"lastPrice"`
	ChangePercent float64 `json:"changePercent"`
}

func (s *PortfolioService) GetDashboard(ctx context.Context, userID uint) (*PortfolioDashboard, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	positions, err := s.repo.GetUserPositions(ctx, userID)
	if err != nil {
		return nil, err
	}

	dashboard := &PortfolioDashboard{
		Balance:   user.Balance,
		Positions: make([]DashboardPosition, 0, len(positions)),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, pos := range positions {
		wg.Add(1)
		go func(position struct{ 
			StockName string
			Symbol string
			Quantity float64
			AvgPrice float64 
		}) {
			defer wg.Done()
			
			dp := DashboardPosition{
				StockName: position.StockName,
				Symbol:    position.Symbol,
				Quantity:  position.Quantity,
				AvgPrice:  position.AvgPrice,
			}

			// Fetch real-time quote
			quoteBytes, err := s.stockProvider.GetStockQuote(ctx, position.Symbol)
			if err == nil {
				var quote avanzaQuote
				if err := json.Unmarshal(quoteBytes, &quote); err == nil {
					dp.CurrentPrice = quote.LastPrice
					dp.ChangePercent = quote.ChangePercent
				}
			}
			
			// Fallback if quote fails
			if dp.CurrentPrice == 0 {
				dp.CurrentPrice = dp.AvgPrice 
			}

			dp.Value = dp.CurrentPrice * dp.Quantity

			mu.Lock()
			dashboard.Positions = append(dashboard.Positions, dp)
			mu.Unlock()

		}(struct{StockName string; Symbol string; Quantity float64; AvgPrice float64}{
			StockName: pos.Stock.Name,
			Symbol:    pos.Stock.Symbol,
			Quantity:  pos.Quantity,
			AvgPrice:  pos.AvgPrice,
		})
	}

	wg.Wait()

	// Calculate totals and weights
	for _, p := range dashboard.Positions {
		dashboard.TotalValue += p.Value
		dashboard.TotalUnrealized += (p.CurrentPrice - p.AvgPrice) * p.Quantity
	}

	if dashboard.TotalValue > 0 {
		for i := range dashboard.Positions {
			dashboard.Positions[i].Weight = (dashboard.Positions[i].Value / dashboard.TotalValue) * 100
		}
	}

	// Sort by day change for Top Movers
	dashboard.TopMovers = make([]DashboardPosition, len(dashboard.Positions))
	copy(dashboard.TopMovers, dashboard.Positions)
	sort.Slice(dashboard.TopMovers, func(i, j int) bool {
		return dashboard.TopMovers[i].ChangePercent > dashboard.TopMovers[j].ChangePercent
	})

	if len(dashboard.TopMovers) > 5 {
		dashboard.TopMovers = dashboard.TopMovers[:5]
	}

	return dashboard, nil
}
