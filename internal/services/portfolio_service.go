package services

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

type PortfolioService struct {
	repo      domain.PortfolioRepository
	provider  domain.MarketDataProvider
	userRepo  domain.UserRepository
	vaultRepo domain.VaultRepository
	vaultAPY  float64
	now       func() time.Time
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

// SetVault attaches vault repository and APY for including vault balance in the portfolio.
func (s *PortfolioService) SetVault(vaultRepo domain.VaultRepository, apy float64, now func() time.Time) {
	s.vaultRepo = vaultRepo
	if apy <= 0 {
		apy = domain.DefaultVaultAPY
	}
	s.vaultAPY = apy
	if now == nil {
		now = time.Now
	}
	s.now = now
}

var hundred = decimal.NewFromInt(100)

// valuedPosition is a position with its live price, kept in decimal until the portfolio is serialized.
type valuedPosition struct {
	orderbookID, symbol, name string
	quantity, avgCost         decimal.Decimal
	lastPrice, dayChange      decimal.Decimal
	marketValue, unrealized   decimal.Decimal
	unrealizedPercent         decimal.Decimal
}

// GetPortfolio returns the user's cash, open positions valued at live quotes, and unrealized and realized
// P&L (api/openapi.yaml#/components/schemas/Portfolio). Quotes are fetched concurrently by orderbook id;
// a failed quote (or one without a positive last price) values the position at its avg_cost with a day
// change of 0. All math is decimal; values are rounded half-up only when the DTO is filled.
func (s *PortfolioService) GetPortfolio(ctx context.Context, userID uint) (*domain.Portfolio, error) {
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
			orderbookID: pos.Stock.OrderbookID,
			symbol:      pos.Stock.Symbol,
			name:        pos.Stock.Name,
			quantity:    pos.Quantity,
			avgCost:     pos.AvgCost,
			lastPrice:   pos.AvgCost, // fallback when the quote fails or has no price
			dayChange:   decimal.Zero,
		}
		wg.Add(1)
		go func(vp *valuedPosition) {
			defer wg.Done()
			// Positions are quoted by their stock's orderbook id (symbols are not unique across markets).
			// Each goroutine writes only its own slice element, so no lock is needed.
			if q := fetchPositionQuote(ctx, s.provider, vp.orderbookID); q.ok {
				vp.lastPrice = q.last
				vp.dayChange = q.dayChange
			}
		}(&valued[i])
	}
	wg.Wait()

	holdingsValue := decimal.Zero
	unrealizedTotal := decimal.Zero
	for i := range valued {
		vp := &valued[i]
		cost := vp.avgCost.Mul(vp.quantity)
		vp.marketValue = vp.lastPrice.Mul(vp.quantity)
		vp.unrealized = vp.marketValue.Sub(cost)
		vp.unrealizedPercent = decimal.Zero
		if !cost.IsZero() {
			vp.unrealizedPercent = vp.unrealized.Div(cost).Mul(hundred)
		}
		holdingsValue = holdingsValue.Add(vp.marketValue)
		unrealizedTotal = unrealizedTotal.Add(vp.unrealized)
	}

	// Largest holding first, then orderbook id for a stable order.
	sort.SliceStable(valued, func(i, j int) bool {
		if c := valued[i].marketValue.Cmp(valued[j].marketValue); c != 0 {
			return c > 0
		}
		return valued[i].orderbookID < valued[j].orderbookID
	})

	vaultValue := decimal.Zero
	if s.vaultRepo != nil {
		now := time.Now
		if s.now != nil {
			now = s.now
		}
		if v, err := s.vaultRepo.GetVaultBalance(ctx, userID, s.vaultAPY, now().UTC()); err == nil {
			vaultValue = v
		}
	}

	portfolio := &domain.Portfolio{
		Cash:          domain.MoneyFloat(user.Balance),
		HoldingsValue: domain.MoneyFloat(holdingsValue),
		VaultValue:    domain.MoneyFloat(vaultValue),
		TotalEquity:   domain.MoneyFloat(user.Balance.Add(holdingsValue).Add(vaultValue)),
		UnrealizedPnL: domain.MoneyFloat(unrealizedTotal),
		RealizedPnL:   domain.MoneyFloat(user.RealizedPnL),
		Positions:     make([]domain.PortfolioPosition, 0, len(valued)),
	}
	for _, vp := range valued {
		weight := decimal.Zero
		if !holdingsValue.IsZero() {
			weight = vp.marketValue.Div(holdingsValue).Mul(hundred)
		}
		portfolio.Positions = append(portfolio.Positions, domain.PortfolioPosition{
			OrderbookID:          vp.orderbookID,
			Symbol:               vp.symbol,
			Name:                 vp.name,
			Quantity:             vp.quantity.Round(4).InexactFloat64(),
			AvgCost:              domain.MoneyFloat(vp.avgCost),
			LastPrice:            domain.MoneyFloat(vp.lastPrice),
			MarketValue:          domain.MoneyFloat(vp.marketValue),
			UnrealizedPnL:        domain.MoneyFloat(vp.unrealized),
			UnrealizedPnLPercent: domain.MoneyFloat(vp.unrealizedPercent),
			DayChangePercent:     domain.MoneyFloat(vp.dayChange),
			Weight:               domain.MoneyFloat(weight),
		})
	}

	// Top movers: biggest day change first, then orderbook id; at most MaxTopMovers.
	order := make([]int, len(valued))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		va, vb := valued[order[a]], valued[order[b]]
		if c := va.dayChange.Cmp(vb.dayChange); c != 0 {
			return c > 0
		}
		return va.orderbookID < vb.orderbookID
	})
	if len(order) > domain.MaxTopMovers {
		order = order[:domain.MaxTopMovers]
	}
	portfolio.TopMovers = make([]domain.PortfolioPosition, 0, len(order))
	for _, idx := range order {
		portfolio.TopMovers = append(portfolio.TopMovers, portfolio.Positions[idx])
	}

	return portfolio, nil
}

// positionQuote is the live price of a held instrument as the portfolio uses it: last and day change rounded
// half-up to 2 decimals. ok is false when the quote failed or had no positive last price; the caller then
// values the position at its avg_cost (with a day change of 0).
type positionQuote struct {
	last, dayChange decimal.Decimal
	ok              bool
}

// fetchPositionQuote quotes orderbookID for valuation. GET /portfolio and the equity snapshots (story NW-10)
// both use it, so a snapshot taken right after a fill equals the portfolio's total_equity.
func fetchPositionQuote(ctx context.Context, provider domain.MarketDataProvider, orderbookID string) positionQuote {
	quote, err := provider.Quote(ctx, orderbookID)
	if err != nil || quote.Last <= 0 {
		return positionQuote{}
	}
	return positionQuote{
		last:      decimal.NewFromFloat(quote.Last).Round(2),
		dayChange: decimal.NewFromFloat(quote.ChangePercent).Round(2),
		ok:        true,
	}
}
