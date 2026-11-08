package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/handlers"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func newLeaderboardRouter(svc *services.LeaderboardService, auth domain.AuthRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	h := handlers.NewLeaderboardHandler(svc, auth)
	h.Register(v1)
	return r
}

func TestLeaderboardHandler_AC2_AC3_AC4(t *testing.T) {
	name1 := "bull_42"
	name2 := "bear_99"
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	u1 := &models.User{ID: 1, DisplayName: &name1, LeaderboardOptIn: true, OptedInAt: &t0}
	u2 := &models.User{ID: 2, DisplayName: &name2, LeaderboardOptIn: true, OptedInAt: &t0}

	users := testutil.NewUserRepo(u1, u2)
	auth := testutil.NewAuthRepo().WithSession("session-1", u1)
	lbRepo := testutil.NewLeaderboardRepo()

	_ = lbRepo.ReplaceEntries(t.Context(), "return", []models.LeaderboardEntry{
		{
			Metric:         "return",
			UserID:         1,
			Rank:           1,
			DisplayName:    "bull_42",
			TotalReturnPct: decimal.NewFromFloat(15.50),
			SharpeRatio:    nil,
			MaxDrawdownPct: nil,
			OptedInAt:      t0,
		},
		{
			Metric:         "return",
			UserID:         2,
			Rank:           2,
			DisplayName:    "bear_99",
			TotalReturnPct: decimal.NewFromFloat(10.00),
			SharpeRatio:    nil,
			MaxDrawdownPct: nil,
			OptedInAt:      t0,
		},
	})

	_ = lbRepo.ReplaceEntries(t.Context(), "sharpe", []models.LeaderboardEntry{
		{
			Metric:         "sharpe",
			UserID:         2,
			Rank:           1,
			DisplayName:    "bear_99",
			TotalReturnPct: decimal.NewFromFloat(10.00),
			SharpeRatio:    func() *decimal.Decimal { d := decimal.NewFromFloat(1.75); return &d }(),
			MaxDrawdownPct: func() *decimal.Decimal { d := decimal.NewFromFloat(-3.20); return &d }(),
			OptedInAt:      t0,
		},
	})

	svc := services.NewLeaderboardService(lbRepo, users, nil)
	router := newLeaderboardRouter(svc, auth)

	t.Run("AC2: exact keys rank display_name total_return_pct sharpe_ratio max_drawdown_pct is_me", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?metric=return", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}

		var body struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if len(body.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(body.Items))
		}

		expectedKeys := map[string]bool{
			"rank":             true,
			"display_name":     true,
			"total_return_pct": true,
			"sharpe_ratio":     true,
			"max_drawdown_pct": true,
			"is_me":            true,
		}

		for _, item := range body.Items {
			if len(item) != len(expectedKeys) {
				t.Errorf("expected exactly %d keys, got %d keys: %+v", len(expectedKeys), len(item), item)
			}
			for k := range item {
				if !expectedKeys[k] {
					t.Errorf("unexpected key %q in item: %+v", k, item)
				}
			}
			// Must never expose sensitive fields
			for _, forbidden := range []string{"id", "user_id", "email", "balance", "google_id"} {
				if _, ok := item[forbidden]; ok {
					t.Errorf("item contains forbidden field %q", forbidden)
				}
			}
		}
	})

	t.Run("AC4: is_me true only on caller entry", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?metric=return", nil)
		req.AddCookie(&http.Cookie{Name: "session_id", Value: "session-1"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}

		var page domain.LeaderboardPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if len(page.Items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(page.Items))
		}

		if !page.Items[0].IsMe || page.Items[0].DisplayName != "bull_42" {
			t.Errorf("expected bull_42 to have is_me=true, got %+v", page.Items[0])
		}
		if page.Items[1].IsMe || page.Items[1].DisplayName != "bear_99" {
			t.Errorf("expected bear_99 to have is_me=false, got %+v", page.Items[1])
		}
	})

	t.Run("AC4: unknown metric returns 400 VALIDATION_ERROR", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?metric=unknown", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}

		var errResp domain.ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &errResp)
		if errResp.Code != domain.CodeValidation {
			t.Errorf("expected VALIDATION_ERROR, got %s", errResp.Code)
		}
	})

	t.Run("invalid limit returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/leaderboard?metric=return&limit=999", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})
}
