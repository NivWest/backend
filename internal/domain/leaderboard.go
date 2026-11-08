package domain

import (
	"context"

	"backend/internal/models"
)

const (
	LeaderboardMetricReturn = "return"
	LeaderboardMetricSharpe = "sharpe"

	DefaultLeaderboardLimit = 50
	MaxLeaderboardLimit     = 100
)

// UpdateProfileRequest is the body of PATCH /api/v1/me (story NW-13).
type UpdateProfileRequest struct {
	DisplayName      *string `json:"display_name"`
	LeaderboardOptIn *bool   `json:"leaderboard_opt_in"`
}

// LeaderboardEntry is one public entry in the leaderboard (story NW-13).
// Every entry contains strictly these 6 keys without internal IDs, emails or balances.
type LeaderboardEntry struct {
	Rank           int      `json:"rank"`
	DisplayName    string   `json:"display_name"`
	TotalReturnPct float64  `json:"total_return_pct"`
	SharpeRatio    *float64 `json:"sharpe_ratio"`
	MaxDrawdownPct *float64 `json:"max_drawdown_pct"`
	IsMe           bool     `json:"is_me"`
}

// LeaderboardPage is the response for GET /api/v1/leaderboard.
type LeaderboardPage struct {
	Items []LeaderboardEntry `json:"items"`
}

// LeaderboardRepository stores and queries materialized leaderboard rankings.
type LeaderboardRepository interface {
	// ReplaceEntries atomically replaces all entries for the given metric with new entries in a single transaction.
	ReplaceEntries(ctx context.Context, metric string, entries []models.LeaderboardEntry) error

	// ListByMetric returns entries for a metric ordered by rank ASC, limited to `limit`.
	ListByMetric(ctx context.Context, metric string, limit int) ([]models.LeaderboardEntry, error)

	// GetByUserAndMetric returns the user's entry for a metric, or ErrLeaderboardEntryNotFound if none.
	GetByUserAndMetric(ctx context.Context, metric string, userID uint) (*models.LeaderboardEntry, error)
}
