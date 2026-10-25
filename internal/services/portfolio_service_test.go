package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

// position is a holding of symbol, whose stock has the orderbook id "OB-" + symbol.
func position(symbol, qty, avg string) models.Position {
	return models.Position{
		Stock:    models.Stock{OrderbookID: "OB-" + symbol, Symbol: symbol, Name: symbol + " Inc"},
		Quantity: dec(qty),
		AvgPrice: dec(avg),
	}
}

func findPosition(t *testing.T, positions []DashboardPosition, symbol string) DashboardPosition {
	t.Helper()
	for _, p := range positions {
		if p.Symbol == symbol {
			return p
		}
	}
	t.Fatalf("position %q not found in %+v", symbol, positions)
	return DashboardPosition{}
}

func TestPortfolioService_GetDashboard_TotalsAndWeights(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: dec("5000")})
	repo := &testutil.PortfolioRepo{Positions: []models.Position{
		position("AAA", "10", "100"), // live 150 -> value 1500, unrealized +500
		position("BBB", "5", "100"),  // live 100 -> value 500, unrealized 0
	}}
	quotes := map[string]domain.Quote{
		"OB-AAA": {Last: 150, ChangePercent: 2.5},
		"OB-BBB": {Last: 100, ChangePercent: -1},
	}
	provider := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		q, ok := quotes[id]
		if !ok {
			return domain.Quote{}, domain.ErrInstrumentNotFound
		}
		return q, nil
	}}

	dash, err := NewPortfolioService(repo, provider, users).GetDashboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}

	if dash.Balance != 5000 {
		t.Errorf("Balance = %v, want 5000", dash.Balance)
	}
	if !almostEqual(dash.TotalValue, 2000) {
		t.Errorf("TotalValue = %v, want 2000", dash.TotalValue)
	}
	if !almostEqual(dash.TotalUnrealized, 500) {
		t.Errorf("TotalUnrealized = %v, want 500", dash.TotalUnrealized)
	}

	aaa := findPosition(t, dash.Positions, "AAA")
	if !almostEqual(aaa.Weight, 75) || aaa.CurrentPrice != 150 || aaa.ChangePercent != 2.5 {
		t.Errorf("AAA = %+v, want weight 75, price 150, change 2.5", aaa)
	}
	bbb := findPosition(t, dash.Positions, "BBB")
	if !almostEqual(bbb.Weight, 25) {
		t.Errorf("BBB weight = %v, want 25", bbb.Weight)
	}
}

func TestPortfolioService_GetDashboard_FallsBackToAvgPriceWhenQuoteFails(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1})
	repo := &testutil.PortfolioRepo{Positions: []models.Position{
		position("ERR", "2", "40"),  // provider error
		position("GONE", "3", "10"), // unknown instrument
		position("ZERO", "1", "99"), // zero price
	}}
	provider := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		switch id {
		case "OB-ERR":
			return domain.Quote{}, fmt.Errorf("%w: upstream down", domain.ErrMarketDataUnavailable)
		case "OB-GONE":
			return domain.Quote{}, domain.ErrInstrumentNotFound
		default:
			return domain.Quote{Last: 0}, nil
		}
	}}

	dash, err := NewPortfolioService(repo, provider, users).GetDashboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	for _, p := range dash.Positions {
		if p.CurrentPrice != p.AvgPrice {
			t.Errorf("%s CurrentPrice = %v, want fallback AvgPrice %v", p.Symbol, p.CurrentPrice, p.AvgPrice)
		}
	}
	if !almostEqual(dash.TotalUnrealized, 0) {
		t.Errorf("TotalUnrealized = %v, want 0", dash.TotalUnrealized)
	}
}

func TestPortfolioService_GetDashboard_TopMoversSortedAndCapped(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1})
	var positions []models.Position
	for i := 1; i <= 7; i++ {
		positions = append(positions, position(fmt.Sprintf("S%d", i), "1", "10"))
	}
	provider := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		var n int
		fmt.Sscanf(id, "OB-S%d", &n)
		return domain.Quote{Last: 10, ChangePercent: float64(n)}, nil
	}}

	dash, err := NewPortfolioService(&testutil.PortfolioRepo{Positions: positions}, provider, users).
		GetDashboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	if len(dash.Positions) != 7 {
		t.Fatalf("len(Positions) = %d, want 7", len(dash.Positions))
	}
	if len(dash.TopMovers) != 5 {
		t.Fatalf("len(TopMovers) = %d, want 5", len(dash.TopMovers))
	}
	for i, want := range []string{"S7", "S6", "S5", "S4", "S3"} {
		if dash.TopMovers[i].Symbol != want {
			t.Errorf("TopMovers[%d] = %s, want %s", i, dash.TopMovers[i].Symbol, want)
		}
	}
}

func TestPortfolioService_GetDashboard_EmptyPortfolio(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: dec("100000")})
	dash, err := NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.MarketProvider{}, users).
		GetDashboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	if dash.TotalValue != 0 || len(dash.Positions) != 0 || len(dash.TopMovers) != 0 {
		t.Errorf("expected empty dashboard, got %+v", dash)
	}
}

func TestPortfolioService_GetDashboard_PropagatesErrors(t *testing.T) {
	t.Run("user not found", func(t *testing.T) {
		svc := NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.MarketProvider{}, testutil.NewUserRepo())
		if _, err := svc.GetDashboard(context.Background(), 42); !errors.Is(err, domain.ErrUserNotFound) {
			t.Fatalf("error = %v, want ErrUserNotFound", err)
		}
	})
	t.Run("positions query fails", func(t *testing.T) {
		repoErr := errors.New("db down")
		svc := NewPortfolioService(&testutil.PortfolioRepo{Err: repoErr}, &testutil.MarketProvider{},
			testutil.NewUserRepo(&models.User{ID: 1}))
		if _, err := svc.GetDashboard(context.Background(), 1); !errors.Is(err, repoErr) {
			t.Fatalf("error = %v, want %v", err, repoErr)
		}
	})
}

func TestPortfolioService_GetDashboard_RoundsMoneyAndKeepsDecimalPrecision(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: dec("98996.50")})
	repo := &testutil.PortfolioRepo{Positions: []models.Position{
		position("FJRD", "0.3333", "100.10"), // live 100.07 -> value 33.353331 -> 33.35
	}}
	provider := &testutil.MarketProvider{QuoteFn: testutil.FixedQuote(domain.Quote{Last: 100.07})}

	dash, err := NewPortfolioService(repo, provider, users).GetDashboard(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	if dash.Balance != 98996.50 {
		t.Errorf("Balance = %v, want 98996.50", dash.Balance)
	}
	p := dash.Positions[0]
	if p.Quantity != 0.3333 || p.AvgPrice != 100.10 || p.CurrentPrice != 100.07 || p.Value != 33.35 {
		t.Errorf("position = %+v, want quantity 0.3333, avg 100.10, price 100.07, value 33.35", p)
	}
	if dash.TotalValue != 33.35 || dash.TotalUnrealized != -0.01 || p.Weight != 100 {
		t.Errorf("totals = %v / %v, weight %v; want 33.35 / -0.01, 100", dash.TotalValue, dash.TotalUnrealized, p.Weight)
	}
}

func TestPortfolioService_GetDashboard_QuotesByOrderbookID(t *testing.T) {
	var asked []string
	var mu sync.Mutex
	provider := &testutil.MarketProvider{QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
		mu.Lock()
		asked = append(asked, id)
		mu.Unlock()
		return domain.Quote{Last: 1}, nil
	}}
	repo := &testutil.PortfolioRepo{Positions: []models.Position{position("VOLV-B", "1", "1")}}
	if _, err := NewPortfolioService(repo, provider, testutil.NewUserRepo(&models.User{ID: 1})).
		GetDashboard(context.Background(), 1); err != nil {
		t.Fatalf("GetDashboard() error = %v", err)
	}
	if len(asked) != 1 || asked[0] != "OB-VOLV-B" {
		t.Errorf("quoted %v, want [OB-VOLV-B] (the orderbook id, not the symbol)", asked)
	}
}
