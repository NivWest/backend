package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func portfolioEngine(users *testutil.UserRepo, positions *testutil.PortfolioRepo, market *testutil.MarketProvider) *gin.Engine {
	return newEngine(NewPortfolioHandler(services.NewPortfolioService(positions, market, users), newAuth()))
}

func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestPortfolioHandler_GetPortfolio(t *testing.T) {
	portfolioKeys := []string{"buying_power", "cash", "holdings_value", "positions", "realized_pnl", "reserved_cash", "top_movers", "total_equity", "unrealized_pnl", "vault_value"}
	positionKeys := []string{"avg_cost", "day_change_percent", "last_price", "market_value", "name", "orderbook_id",
		"quantity", "reserved_quantity", "symbol", "unrealized_pnl", "unrealized_pnl_percent", "weight"}

	t.Run("requires a session", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1})
		w := do(t, portfolioEngine(users, &testutil.PortfolioRepo{}, &testutil.MarketProvider{}), http.MethodGet, "/api/v1/portfolio", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	})

	t.Run("old /portfolio/dashboard is gone", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1})
		w := do(t, portfolioEngine(users, &testutil.PortfolioRepo{}, &testutil.MarketProvider{}), http.MethodGet,
			"/api/v1/portfolio/dashboard", memberSession, nil)
		expectStatus(t, w, http.StatusNotFound)
	})

	t.Run("empty portfolio returns cash and [] arrays", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.RequireFromString("100000")})
		w := do(t, portfolioEngine(users, &testutil.PortfolioRepo{}, &testutil.MarketProvider{}), http.MethodGet,
			"/api/v1/portfolio", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		raw := w.Body.String()
		if !strings.Contains(raw, `"positions":[]`) || !strings.Contains(raw, `"top_movers":[]`) {
			t.Errorf("positions and top_movers must serialize as [], got %s", raw)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode portfolio: %v (body %s)", err, raw)
		}
		if got := sortedKeys(body); !reflect.DeepEqual(got, portfolioKeys) {
			t.Errorf("portfolio keys = %v, want %v", got, portfolioKeys)
		}
		want := map[string]any{"cash": float64(100000), "holdings_value": float64(0), "total_equity": float64(100000),
			"unrealized_pnl": float64(0), "realized_pnl": float64(0), "vault_value": float64(0)}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("portfolio[%q] = %v, want %v", k, body[k], v)
			}
		}
	})

	t.Run("returns the story NW-05 portfolio", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.RequireFromString("98996.50")})
		positions := &testutil.PortfolioRepo{Positions: []models.Position{{
			Stock:    models.Stock{OrderbookID: "SIM-NVE", Symbol: "NVE", Name: "Nordvik Energy"},
			Quantity: decimal.RequireFromString("10"),
			AvgCost:  decimal.RequireFromString("100.35"),
		}}}
		market := &testutil.MarketProvider{QuoteFn: testutil.FixedQuote(domain.Quote{Last: 110, ChangePercent: 1.25})}

		w := do(t, portfolioEngine(users, positions, market), http.MethodGet, "/api/v1/portfolio", memberSession, nil)
		expectStatus(t, w, http.StatusOK)

		var body struct {
			Cash          float64          `json:"cash"`
			HoldingsValue float64          `json:"holdings_value"`
			TotalEquity   float64          `json:"total_equity"`
			UnrealizedPnL float64          `json:"unrealized_pnl"`
			RealizedPnL   float64          `json:"realized_pnl"`
			Positions     []map[string]any `json:"positions"`
			TopMovers     []map[string]any `json:"top_movers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode portfolio: %v (body %s)", err, w.Body.String())
		}
		if body.Cash != 98996.50 || body.HoldingsValue != 1100 || body.TotalEquity != 100096.50 ||
			body.UnrealizedPnL != 96.50 || body.RealizedPnL != 0 {
			t.Errorf("totals = %s", w.Body.String())
		}
		if len(body.Positions) != 1 || len(body.TopMovers) != 1 {
			t.Fatalf("want one position and one mover, got %s", w.Body.String())
		}
		pos := body.Positions[0]
		if got := sortedKeys(pos); !reflect.DeepEqual(got, positionKeys) {
			t.Errorf("position keys = %v, want %v", got, positionKeys)
		}
		want := map[string]any{
			"orderbook_id": "SIM-NVE", "symbol": "NVE", "name": "Nordvik Energy", "quantity": float64(10),
			"avg_cost": 100.35, "last_price": float64(110), "market_value": float64(1100), "unrealized_pnl": 96.50,
			"unrealized_pnl_percent": 9.62, "day_change_percent": 1.25, "weight": float64(100),
		}
		for k, v := range want {
			if pos[k] != v {
				t.Errorf("position[%q] = %v, want %v", k, pos[k], v)
			}
		}
		if !reflect.DeepEqual(body.TopMovers[0], pos) {
			t.Errorf("top mover = %v, want %v", body.TopMovers[0], pos)
		}
	})

	t.Run("service error returns 500 INTERNAL_ERROR", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1})
		w := do(t, portfolioEngine(users, &testutil.PortfolioRepo{Err: errors.New("db down")}, &testutil.MarketProvider{}),
			http.MethodGet, "/api/v1/portfolio", memberSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorCode(t, w, domain.CodeInternal)
	})

	t.Run("missing user returns 500 INTERNAL_ERROR", func(t *testing.T) {
		users := testutil.NewUserRepo()
		users.GetByIDFn = func(context.Context, uint) (*models.User, error) { return nil, domain.ErrUserNotFound }
		w := do(t, portfolioEngine(users, &testutil.PortfolioRepo{}, &testutil.MarketProvider{}), http.MethodGet,
			"/api/v1/portfolio", memberSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorCode(t, w, domain.CodeInternal)
	})
}
