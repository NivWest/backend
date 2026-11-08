package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

var analyticsNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// analyticsEngine wires the analytics handler on fakes: user 1 (the member session) holds 100 SIM-FJRD at last 100
// with the given cash.
func analyticsEngine(cash string, positions []models.Position, snaps *testutil.SnapshotRepo) *gin.Engine {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.RequireFromString(cash)})
	market := &testutil.MarketProvider{QuoteFn: testutil.FixedQuote(domain.Quote{Last: 100})}
	portfolio := services.NewPortfolioService(&testutil.PortfolioRepo{Positions: positions}, market, users)
	svc := services.NewAnalyticsService(snaps, portfolio, domain.DefaultRiskFreeRate, func() time.Time { return analyticsNow })
	return newEngine(NewAnalyticsHandler(svc, newAuth()))
}

func TestAnalyticsHandler_RequiresSession(t *testing.T) {
	r := analyticsEngine("100000", nil, &testutil.SnapshotRepo{})
	for _, path := range []string{"/api/v1/portfolio/history", "/api/v1/portfolio/history?range=1w", "/api/v1/portfolio/analytics"} {
		w := do(t, r, http.MethodGet, path, "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	}
}

func TestAnalyticsHandler_GetHistory(t *testing.T) {
	snaps := &testutil.SnapshotRepo{Snapshots: []models.EquitySnapshot{
		{UserID: 1, T: analyticsNow.Add(-2 * time.Hour), Equity: decimal.RequireFromString("100000")},
		{UserID: 1, T: analyticsNow.Add(-time.Hour), Equity: decimal.RequireFromString("99964.97")},
		{UserID: 2, T: analyticsNow.Add(-time.Hour), Equity: decimal.RequireFromString("1")},
	}}
	r := analyticsEngine("100000", nil, snaps)

	tests := []struct {
		query      string
		wantRange  string
		wantPoints int
	}{
		// all keeps the last snapshot of the day only.
		{"", "1d", 2}, {"?range=1d", "1d", 2}, {"?range=1w", "1w", 2}, {"?range=1m", "1m", 2}, {"?range=all", "all", 1},
	}
	for _, tt := range tests {
		t.Run("ok "+tt.query, func(t *testing.T) {
			w := do(t, r, http.MethodGet, "/api/v1/portfolio/history"+tt.query, memberSession, nil)
			expectStatus(t, w, http.StatusOK)
			var body struct {
				Range  string `json:"range"`
				Points []struct {
					T      string  `json:"t"`
					Equity float64 `json:"equity"`
				} `json:"points"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v (%s)", err, w.Body)
			}
			if body.Range != tt.wantRange || len(body.Points) != tt.wantPoints {
				t.Fatalf("body = %s", w.Body)
			}
			if lastPt := body.Points[len(body.Points)-1]; lastPt.Equity != 99964.97 || lastPt.T != "2026-11-08T11:00:00Z" {
				t.Errorf("last point = %+v, want 99964.97 at 2026-11-08T11:00:00Z", lastPt)
			}
		})
	}

	for _, bad := range []string{"?range=", "?range=1y", "?range=ALL", "?range=1d%20"} {
		t.Run("400 "+bad, func(t *testing.T) {
			w := do(t, r, http.MethodGet, "/api/v1/portfolio/history"+bad, memberSession, nil)
			expectStatus(t, w, http.StatusBadRequest)
			expectErrorCode(t, w, domain.CodeValidation)
		})
	}

	t.Run("500 on a repository error", func(t *testing.T) {
		failing := &testutil.SnapshotRepo{ListByUserFn: func(context.Context, uint, time.Time) ([]models.EquitySnapshot, error) {
			return nil, errors.New("db down")
		}}
		w := do(t, analyticsEngine("1", nil, failing), http.MethodGet, "/api/v1/portfolio/history", memberSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorCode(t, w, domain.CodeInternal)
	})
}

func TestAnalyticsHandler_GetAnalytics(t *testing.T) {
	t.Run("fresh account", func(t *testing.T) {
		w := do(t, analyticsEngine("100000", nil, &testutil.SnapshotRepo{}), http.MethodGet, "/api/v1/portfolio/analytics", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"total_return_pct": float64(0), "volatility_annualized_pct": nil, "sharpe_ratio": nil, "max_drawdown_pct": nil,
			"sufficient_data": false, "data_points": float64(0),
			"allocation": []any{map[string]any{"label": "Cash", "orderbook_id": nil, "value": float64(100000), "weight": float64(100)}},
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body = %s", w.Body)
		}
	})

	t.Run("ac2 after buying 100 SIM-FJRD", func(t *testing.T) {
		positions := []models.Position{{
			Stock:    models.Stock{OrderbookID: "SIM-FJRD", Symbol: "FJRD", Name: "Fjord Robotics"},
			Quantity: decimal.RequireFromString("100"), AvgCost: decimal.RequireFromString("100.3503"),
		}}
		w := do(t, analyticsEngine("89964.97", positions, &testutil.SnapshotRepo{}), http.MethodGet, "/api/v1/portfolio/analytics", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		var body domain.PortfolioAnalytics
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Allocation) != 2 || body.Allocation[0].Label != "FJRD" || body.Allocation[0].Weight != 10 ||
			body.Allocation[0].Value != 10000 || *body.Allocation[0].OrderbookID != "SIM-FJRD" ||
			body.Allocation[1].Label != "Cash" || body.Allocation[1].Weight != 90 || body.Allocation[1].Value != 89964.97 {
			t.Errorf("allocation = %s", w.Body)
		}
		if body.TotalReturnPct != -0.04 {
			t.Errorf("total_return_pct = %v, want -0.04 (99964.97 vs 100000)", body.TotalReturnPct)
		}
	})

	t.Run("500 on a portfolio error", func(t *testing.T) {
		users := testutil.NewUserRepo() // user 1 missing
		portfolio := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.MarketProvider{}, users)
		svc := services.NewAnalyticsService(&testutil.SnapshotRepo{}, portfolio, 0.02, nil)
		w := do(t, newEngine(NewAnalyticsHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/analytics", memberSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorCode(t, w, domain.CodeInternal)
	})
}
