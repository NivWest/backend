package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
)

type fakeAnalyticsProvider struct {
	data map[uint]*domain.PortfolioAnalytics
}

func (f *fakeAnalyticsProvider) GetAnalytics(ctx context.Context, userID uint) (*domain.PortfolioAnalytics, error) {
	if an, ok := f.data[userID]; ok {
		return an, nil
	}
	return nil, errors.New("user not found")
}

func ptr[T any](v T) *T { return &v }

func TestLeaderboardService_RefreshAndRank(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t0.Add(2 * time.Hour)

	name1 := "bull_42"
	name2 := "bear_99"
	name3 := "whale_01"
	name4 := "quiet_trader"

	u1 := &models.User{
		ID:               1,
		DisplayName:      &name1,
		LeaderboardOptIn: true,
		OptedInAt:        &t0,
	}
	u2 := &models.User{
		ID:               2,
		DisplayName:      &name2,
		LeaderboardOptIn: true,
		OptedInAt:        &t1,
	}
	u3 := &models.User{
		ID:               3,
		DisplayName:      &name3,
		LeaderboardOptIn: true,
		OptedInAt:        &t2,
	}
	// u4 not opted in
	u4 := &models.User{
		ID:               4,
		DisplayName:      &name4,
		LeaderboardOptIn: false,
	}

	userRepo := testutil.NewUserRepo(u1, u2, u3, u4)
	lbRepo := testutil.NewLeaderboardRepo()

	analytics := &fakeAnalyticsProvider{
		data: map[uint]*domain.PortfolioAnalytics{
			1: {
				TotalReturnPct: 15.50,
				SufficientData: true,
				SharpeRatio:    ptr(1.85),
				MaxDrawdownPct: ptr(-5.20),
			},
			2: {
				TotalReturnPct: 25.00,
				SufficientData: false, // Not enough data for Sharpe
				SharpeRatio:    nil,
				MaxDrawdownPct: nil,
			},
			3: {
				TotalReturnPct: 15.50, // Tied with u1, but opted in later (t2 > t0)
				SufficientData: true,
				SharpeRatio:    ptr(2.10), // Higher Sharpe than u1
				MaxDrawdownPct: ptr(-2.10),
			},
			4: {
				TotalReturnPct: 99.00,
				SufficientData: true,
				SharpeRatio:    ptr(5.00),
			},
		},
	}

	svc := services.NewLeaderboardService(lbRepo, userRepo, analytics)

	n, err := svc.RefreshLeaderboard(context.Background())
	if err != nil {
		t.Fatalf("RefreshLeaderboard failed: %v", err)
	}
	if n == 0 {
		t.Fatalf("expected entries to be materialized, got %d", n)
	}

	t.Run("metric=return ranks by total_return_pct desc with tie breaking", func(t *testing.T) {
		page, err := svc.GetLeaderboard(context.Background(), "return", 50, 1)
		if err != nil {
			t.Fatalf("GetLeaderboard: %v", err)
		}

		// u4 is not opted in -> only 3 entries
		if len(page.Items) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(page.Items))
		}

		// Rank 1: u2 (25.00%)
		if page.Items[0].DisplayName != "bear_99" || page.Items[0].Rank != 1 {
			t.Errorf("rank 1: want bear_99, got %s (rank %d)", page.Items[0].DisplayName, page.Items[0].Rank)
		}
		if page.Items[0].IsMe {
			t.Errorf("bear_99 is not caller (caller is 1)")
		}

		// Rank 2: u1 (15.50%, opted in at t0) vs u3 (15.50%, opted in at t2)
		// Tie breaks by earlier opted_in_at -> u1 is Rank 2
		if page.Items[1].DisplayName != "bull_42" || page.Items[1].Rank != 2 {
			t.Errorf("rank 2: want bull_42 (earlier opt-in tie break), got %s (rank %d)", page.Items[1].DisplayName, page.Items[1].Rank)
		}
		if !page.Items[1].IsMe {
			t.Errorf("bull_42 should have is_me = true for caller 1")
		}

		// Rank 3: u3 (15.50%)
		if page.Items[2].DisplayName != "whale_01" || page.Items[2].Rank != 3 {
			t.Errorf("rank 3: want whale_01, got %s (rank %d)", page.Items[2].DisplayName, page.Items[2].Rank)
		}
	})

	t.Run("metric=sharpe excludes insufficient_data users", func(t *testing.T) {
		page, err := svc.GetLeaderboard(context.Background(), "sharpe", 50, 2)
		if err != nil {
			t.Fatalf("GetLeaderboard: %v", err)
		}

		// u2 has insufficient data -> only u3 and u1 appear
		if len(page.Items) != 2 {
			t.Fatalf("expected 2 entries on sharpe, got %d", len(page.Items))
		}

		// Rank 1: u3 (Sharpe 2.10)
		if page.Items[0].DisplayName != "whale_01" || page.Items[0].Rank != 1 {
			t.Errorf("rank 1: want whale_01, got %s", page.Items[0].DisplayName)
		}

		// Rank 2: u1 (Sharpe 1.85)
		if page.Items[1].DisplayName != "bull_42" || page.Items[1].Rank != 2 {
			t.Errorf("rank 2: want bull_42, got %s", page.Items[1].DisplayName)
		}

		// u2 was caller, but has insufficient data, so not on leaderboard and not appended
		for _, item := range page.Items {
			if item.IsMe {
				t.Errorf("caller 2 should not be present on sharpe leaderboard")
			}
		}
	})

	t.Run("caller appended if outside limit", func(t *testing.T) {
		// Limit = 1, caller is u1 (who is rank 2 on return)
		page, err := svc.GetLeaderboard(context.Background(), "return", 1, 1)
		if err != nil {
			t.Fatalf("GetLeaderboard: %v", err)
		}

		// Should have 1 entry from limit + 1 caller entry appended = 2 entries
		if len(page.Items) != 2 {
			t.Fatalf("expected 2 entries (top 1 + caller), got %d", len(page.Items))
		}
		if page.Items[0].DisplayName != "bear_99" || page.Items[0].IsMe {
			t.Errorf("item 0: want bear_99 (is_me false)")
		}
		if page.Items[1].DisplayName != "bull_42" || !page.Items[1].IsMe || page.Items[1].Rank != 2 {
			t.Errorf("item 1: want bull_42 (is_me true, rank 2)")
		}
	})

	t.Run("invalid metric returns error", func(t *testing.T) {
		_, err := svc.GetLeaderboard(context.Background(), "unknown", 10, 0)
		if !errors.Is(err, domain.ErrInvalidMetric) {
			t.Errorf("want ErrInvalidMetric, got %v", err)
		}
	})
}
