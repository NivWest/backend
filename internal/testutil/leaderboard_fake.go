package testutil

import (
	"context"
	"sync"

	"backend/internal/domain"
	"backend/internal/models"
)

var _ domain.LeaderboardRepository = (*LeaderboardRepo)(nil)

// LeaderboardRepo is a fake in-memory implementation of domain.LeaderboardRepository.
type LeaderboardRepo struct {
	mu      sync.Mutex
	Entries map[string][]models.LeaderboardEntry

	ReplaceEntriesFn     func(ctx context.Context, metric string, entries []models.LeaderboardEntry) error
	ListByMetricFn       func(ctx context.Context, metric string, limit int) ([]models.LeaderboardEntry, error)
	GetByUserAndMetricFn func(ctx context.Context, metric string, userID uint) (*models.LeaderboardEntry, error)
}

// NewLeaderboardRepo initializes a fake leaderboard repository.
func NewLeaderboardRepo() *LeaderboardRepo {
	return &LeaderboardRepo{
		Entries: make(map[string][]models.LeaderboardEntry),
	}
}

func (r *LeaderboardRepo) ReplaceEntries(ctx context.Context, metric string, entries []models.LeaderboardEntry) error {
	if r.ReplaceEntriesFn != nil {
		return r.ReplaceEntriesFn(ctx, metric, entries)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := make([]models.LeaderboardEntry, len(entries))
	copy(copied, entries)
	r.Entries[metric] = copied
	return nil
}

func (r *LeaderboardRepo) ListByMetric(ctx context.Context, metric string, limit int) ([]models.LeaderboardEntry, error) {
	if r.ListByMetricFn != nil {
		return r.ListByMetricFn(ctx, metric, limit)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := r.Entries[metric]
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	copied := make([]models.LeaderboardEntry, len(entries))
	copy(copied, entries)
	return copied, nil
}

func (r *LeaderboardRepo) GetByUserAndMetric(ctx context.Context, metric string, userID uint) (*models.LeaderboardEntry, error) {
	if r.GetByUserAndMetricFn != nil {
		return r.GetByUserAndMetricFn(ctx, metric, userID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.Entries[metric] {
		if e.UserID == userID {
			copy := e
			return &copy, nil
		}
	}
	return nil, domain.ErrLeaderboardEntryNotFound
}
