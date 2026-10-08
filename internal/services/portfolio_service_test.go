package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

func position(symbol string, qty, avg float64) models.Position {
	return models.Position{Stock: models.Stock{Symbol: symbol, Name: symbol + " Inc"}, Quantity: qty, AvgPrice: avg}
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
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: 5000})
	repo := &testutil.PortfolioRepo{Positions: []models.Position{
		position("AAA", 10, 100), // live 150 -> value 1500, unrealized +500
		position("BBB", 5, 100),  // live 100 -> value 500, unrealized 0
	}}
	quotes := map[string]string{
		"AAA": `{"last":150,"changePercent":2.5}`,
		"BBB": `{"last":100,"changePercent":-1}`,
	}
	provider := &testutil.StockProvider{GetStockQuoteFn: func(_ context.Context, id string) ([]byte, error) {
		return []byte(quotes[id]), nil
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
		position("ERR", 2, 40),  // provider error
		position("BAD", 3, 10),  // malformed JSON
		position("ZERO", 1, 99), // zero price
	}}
	provider := &testutil.StockProvider{GetStockQuoteFn: func(_ context.Context, id string) ([]byte, error) {
		switch id {
		case "ERR":
			return nil, errors.New("upstream down")
		case "BAD":
			return []byte("{"), nil
		default:
			return []byte(`{"last":0}`), nil
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
		positions = append(positions, position(fmt.Sprintf("S%d", i), 1, 10))
	}
	provider := &testutil.StockProvider{GetStockQuoteFn: func(_ context.Context, id string) ([]byte, error) {
		var n int
		fmt.Sscanf(id, "S%d", &n)
		return []byte(fmt.Sprintf(`{"last":10,"changePercent":%d}`, n)), nil
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
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: 100000})
	dash, err := NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.StockProvider{}, users).
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
		svc := NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.StockProvider{}, testutil.NewUserRepo())
		if _, err := svc.GetDashboard(context.Background(), 42); !errors.Is(err, domain.ErrUserNotFound) {
			t.Fatalf("error = %v, want ErrUserNotFound", err)
		}
	})
	t.Run("positions query fails", func(t *testing.T) {
		repoErr := errors.New("db down")
		svc := NewPortfolioService(&testutil.PortfolioRepo{Err: repoErr}, &testutil.StockProvider{},
			testutil.NewUserRepo(&models.User{ID: 1}))
		if _, err := svc.GetDashboard(context.Background(), 1); !errors.Is(err, repoErr) {
			t.Fatalf("error = %v, want %v", err, repoErr)
		}
	})
}
