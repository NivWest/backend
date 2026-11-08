package services

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"github.com/shopspring/decimal"
)

// AnalyticsProvider supplies risk analytics for users (satisfied by *AnalyticsService).
type AnalyticsProvider interface {
	GetAnalytics(ctx context.Context, userID uint) (*domain.PortfolioAnalytics, error)
}

// LeaderboardService manages leaderboard queries and periodic background materialization (story NW-13).
type LeaderboardService struct {
	leaderboard domain.LeaderboardRepository
	users       domain.UserRepository
	analytics   AnalyticsProvider
	now         func() time.Time
}

func NewLeaderboardService(
	leaderboard domain.LeaderboardRepository,
	users domain.UserRepository,
	analytics AnalyticsProvider,
) *LeaderboardService {
	return &LeaderboardService{
		leaderboard: leaderboard,
		users:       users,
		analytics:   analytics,
		now:         time.Now,
	}
}

// GetLeaderboard returns the materialized leaderboard for the given metric (return or sharpe).
func (s *LeaderboardService) GetLeaderboard(ctx context.Context, metric string, limit int, callerID uint) (*domain.LeaderboardPage, error) {
	if metric != domain.LeaderboardMetricReturn && metric != domain.LeaderboardMetricSharpe {
		return nil, domain.ErrInvalidMetric
	}

	if limit <= 0 {
		limit = domain.DefaultLeaderboardLimit
	}
	if limit > domain.MaxLeaderboardLimit {
		limit = domain.MaxLeaderboardLimit
	}

	entries, err := s.leaderboard.ListByMetric(ctx, metric, limit)
	if err != nil {
		return nil, fmt.Errorf("list leaderboard: %w", err)
	}

	items := make([]domain.LeaderboardEntry, len(entries))
	var callerIncluded bool
	for i, e := range entries {
		isMe := (callerID != 0 && e.UserID == callerID)
		if isMe {
			callerIncluded = true
		}
		var sharpe *float64
		if e.SharpeRatio != nil {
			v := e.SharpeRatio.InexactFloat64()
			sharpe = &v
		}
		var maxDD *float64
		if e.MaxDrawdownPct != nil {
			v := e.MaxDrawdownPct.InexactFloat64()
			maxDD = &v
		}
		items[i] = domain.LeaderboardEntry{
			Rank:           e.Rank,
			DisplayName:    e.DisplayName,
			TotalReturnPct: e.TotalReturnPct.InexactFloat64(),
			SharpeRatio:    sharpe,
			MaxDrawdownPct: maxDD,
			IsMe:           isMe,
		}
	}

	// If caller is authenticated and opted in, but ranked outside the page, append their entry.
	if callerID != 0 && !callerIncluded {
		callerEntry, err := s.leaderboard.GetByUserAndMetric(ctx, metric, callerID)
		if err == nil && callerEntry != nil {
			var sharpe *float64
			if callerEntry.SharpeRatio != nil {
				v := callerEntry.SharpeRatio.InexactFloat64()
				sharpe = &v
			}
			var maxDD *float64
			if callerEntry.MaxDrawdownPct != nil {
				v := callerEntry.MaxDrawdownPct.InexactFloat64()
				maxDD = &v
			}
			items = append(items, domain.LeaderboardEntry{
				Rank:           callerEntry.Rank,
				DisplayName:    callerEntry.DisplayName,
				TotalReturnPct: callerEntry.TotalReturnPct.InexactFloat64(),
				SharpeRatio:    sharpe,
				MaxDrawdownPct: maxDD,
				IsMe:           true,
			})
		}
	}

	return &domain.LeaderboardPage{Items: items}, nil
}

type userCandidate struct {
	user      models.User
	analytics *domain.PortfolioAnalytics
	optedInAt time.Time
}

// RefreshLeaderboard recalculates and atomically replaces materialized rankings for all metrics (story NW-13).
func (s *LeaderboardService) RefreshLeaderboard(ctx context.Context) (int, error) {
	users, err := s.users.ListOptedInUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("list opted in users: %w", err)
	}

	var candidates []userCandidate
	for _, u := range users {
		an, err := s.analytics.GetAnalytics(ctx, u.ID)
		if err != nil {
			log.Printf("leaderboard refresh: get analytics for user %d failed: %v", u.ID, err)
			continue
		}
		optedInAt := u.CreatedAt
		if u.OptedInAt != nil {
			optedInAt = *u.OptedInAt
		}
		candidates = append(candidates, userCandidate{
			user:      u,
			analytics: an,
			optedInAt: optedInAt,
		})
	}

	// 1. Materialize metric=return
	// All opted-in users ranked by total_return_pct desc, ties broken by earlier opted_in_at.
	sort.Slice(candidates, func(i, j int) bool {
		rI := candidates[i].analytics.TotalReturnPct
		rJ := candidates[j].analytics.TotalReturnPct
		if rI != rJ {
			return rI > rJ
		}
		if !candidates[i].optedInAt.Equal(candidates[j].optedInAt) {
			return candidates[i].optedInAt.Before(candidates[j].optedInAt)
		}
		return candidates[i].user.ID < candidates[j].user.ID
	})

	now := s.now()
	returnEntries := make([]models.LeaderboardEntry, len(candidates))
	for i, c := range candidates {
		dispName := ""
		if c.user.DisplayName != nil {
			dispName = *c.user.DisplayName
		}
		retDec := decimal.NewFromFloat(c.analytics.TotalReturnPct).Round(2)
		var sharpeDec *decimal.Decimal
		if c.analytics.SharpeRatio != nil {
			d := decimal.NewFromFloat(*c.analytics.SharpeRatio).Round(2)
			sharpeDec = &d
		}
		var maxDDDec *decimal.Decimal
		if c.analytics.MaxDrawdownPct != nil {
			d := decimal.NewFromFloat(*c.analytics.MaxDrawdownPct).Round(2)
			maxDDDec = &d
		}

		returnEntries[i] = models.LeaderboardEntry{
			Metric:         domain.LeaderboardMetricReturn,
			UserID:         c.user.ID,
			Rank:           i + 1,
			DisplayName:    dispName,
			TotalReturnPct: retDec,
			SharpeRatio:    sharpeDec,
			MaxDrawdownPct: maxDDDec,
			OptedInAt:      c.optedInAt,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}

	if err := s.leaderboard.ReplaceEntries(ctx, domain.LeaderboardMetricReturn, returnEntries); err != nil {
		return 0, fmt.Errorf("replace return entries: %w", err)
	}

	// 2. Materialize metric=sharpe
	// Ranks only users with sufficient_data, by sharpe_ratio desc, ties broken by earlier opted_in_at.
	var sharpeCandidates []userCandidate
	for _, c := range candidates {
		if c.analytics.SufficientData && c.analytics.SharpeRatio != nil {
			sharpeCandidates = append(sharpeCandidates, c)
		}
	}

	sort.Slice(sharpeCandidates, func(i, j int) bool {
		sI := *sharpeCandidates[i].analytics.SharpeRatio
		sJ := *sharpeCandidates[j].analytics.SharpeRatio
		if sI != sJ {
			return sI > sJ
		}
		if !sharpeCandidates[i].optedInAt.Equal(sharpeCandidates[j].optedInAt) {
			return sharpeCandidates[i].optedInAt.Before(sharpeCandidates[j].optedInAt)
		}
		return sharpeCandidates[i].user.ID < sharpeCandidates[j].user.ID
	})

	sharpeEntries := make([]models.LeaderboardEntry, len(sharpeCandidates))
	for i, c := range sharpeCandidates {
		dispName := ""
		if c.user.DisplayName != nil {
			dispName = *c.user.DisplayName
		}
		retDec := decimal.NewFromFloat(c.analytics.TotalReturnPct).Round(2)
		var sharpeDec *decimal.Decimal
		if c.analytics.SharpeRatio != nil {
			d := decimal.NewFromFloat(*c.analytics.SharpeRatio).Round(2)
			sharpeDec = &d
		}
		var maxDDDec *decimal.Decimal
		if c.analytics.MaxDrawdownPct != nil {
			d := decimal.NewFromFloat(*c.analytics.MaxDrawdownPct).Round(2)
			maxDDDec = &d
		}

		sharpeEntries[i] = models.LeaderboardEntry{
			Metric:         domain.LeaderboardMetricSharpe,
			UserID:         c.user.ID,
			Rank:           i + 1,
			DisplayName:    dispName,
			TotalReturnPct: retDec,
			SharpeRatio:    sharpeDec,
			MaxDrawdownPct: maxDDDec,
			OptedInAt:      c.optedInAt,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}

	if err := s.leaderboard.ReplaceEntries(ctx, domain.LeaderboardMetricSharpe, sharpeEntries); err != nil {
		return 0, fmt.Errorf("replace sharpe entries: %w", err)
	}

	return len(returnEntries) + len(sharpeEntries), nil
}
