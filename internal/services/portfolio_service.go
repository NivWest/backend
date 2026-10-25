package services

import (
	"context"
	"sort"
	"sync"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

type PortfolioService struct {
	repo     domain.PortfolioRepository
	provider domain.MarketDataProvider
	userRepo domain.UserRepository
}

func NewPortfolioService(
	repo domain.PortfolioRepository,
	provider domain.MarketDataProvider,
	userRepo domain.UserRepository,
) *PortfolioService {
	return &PortfolioService{
		repo:     repo,
		provider: provider,
		userRepo: userRepo,
	}
}

// DashboardPosition is one holding on the dashboard. Money fields (AvgPrice, CurrentPrice, Value) are SEK
// rounded half-up to 2 decimals; Weight is the percentage of the portfolio's total value.
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

// PortfolioDashboard is the body of GET /portfolio/dashboard. Money fields are SEK rounded half-up to 2 decimals.
type PortfolioDashboard struct {
	Balance         float64             `json:"balance"`
	TotalValue      float64             `json:"total_value"`
	TotalUnrealized float64             `json:"total_unrealized"`
	Positions       []DashboardPosition `json:"positions"`
	TopMovers       []DashboardPosition `json:"top_movers"`
}

// valuedPosition is a position with its live price, kept in decimal until the dashboard is serialized.
type valuedPosition struct {
	name, symbol        string
	quantity, avgPrice  decimal.Decimal
	currentPrice, value decimal.Decimal
	changePercent       float64
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

	valued := make([]valuedPosition, len(positions))
	var wg sync.WaitGroup
	for i, pos := range positions {
		valued[i] = valuedPosition{
			name:         pos.Stock.Name,
			symbol:       pos.Stock.Symbol,
			quantity:     pos.Quantity,
			avgPrice:     pos.AvgPrice,
			currentPrice: pos.AvgPrice, // fallback when the quote fails or has no price
		}
		wg.Add(1)
		go func(vp *valuedPosition, orderbookID string) {
			defer wg.Done()
			// Positions are quoted by their stock's market data orderbook id (symbols are not unique).
			// Each goroutine writes only its own slice element, so no lock is needed.
			if quote, err := s.provider.Quote(ctx, orderbookID); err == nil && quote.Last > 0 {
				vp.currentPrice = decimal.NewFromFloat(quote.Last)
				vp.changePercent = quote.ChangePercent
			}
		}(&valued[i], pos.Stock.OrderbookID)
	}
	wg.Wait()

	totalValue := decimal.Zero
	totalUnrealized := decimal.Zero
	for i := range valued {
		vp := &valued[i]
		vp.value = vp.currentPrice.Mul(vp.quantity)
		totalValue = totalValue.Add(vp.value)
		totalUnrealized = totalUnrealized.Add(vp.currentPrice.Sub(vp.avgPrice).Mul(vp.quantity))
	}

	dashboard := &PortfolioDashboard{
		Balance:         domain.MoneyFloat(user.Balance),
		TotalValue:      domain.MoneyFloat(totalValue),
		TotalUnrealized: domain.MoneyFloat(totalUnrealized),
		Positions:       make([]DashboardPosition, 0, len(valued)),
	}
	for _, vp := range valued {
		dp := DashboardPosition{
			StockName:     vp.name,
			Symbol:        vp.symbol,
			Quantity:      vp.quantity.InexactFloat64(),
			AvgPrice:      domain.MoneyFloat(vp.avgPrice),
			CurrentPrice:  domain.MoneyFloat(vp.currentPrice),
			ChangePercent: vp.changePercent,
			Value:         domain.MoneyFloat(vp.value),
		}
		if totalValue.IsPositive() {
			dp.Weight = vp.value.Div(totalValue).Mul(decimal.NewFromInt(100)).InexactFloat64()
		}
		dashboard.Positions = append(dashboard.Positions, dp)
	}

	// Sort by day change for Top Movers
	dashboard.TopMovers = make([]DashboardPosition, len(dashboard.Positions))
	copy(dashboard.TopMovers, dashboard.Positions)
	sort.SliceStable(dashboard.TopMovers, func(i, j int) bool {
		return dashboard.TopMovers[i].ChangePercent > dashboard.TopMovers[j].ChangePercent
	})

	if len(dashboard.TopMovers) > 5 {
		dashboard.TopMovers = dashboard.TopMovers[:5]
	}

	return dashboard, nil
}
