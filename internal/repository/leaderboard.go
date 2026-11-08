package repository

import (
	"context"
	"errors"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

type leaderboardRepo struct {
	db *gorm.DB
}

// NewLeaderboardRepository creates a concrete domain.LeaderboardRepository backed by GORM.
func NewLeaderboardRepository(db *gorm.DB) domain.LeaderboardRepository {
	return &leaderboardRepo{db: db}
}

// ReplaceEntries atomically deletes old entries for the metric and inserts new entries in one transaction.
func (r *leaderboardRepo) ReplaceEntries(ctx context.Context, metric string, entries []models.LeaderboardEntry) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("metric = ?", metric).Delete(&models.LeaderboardEntry{}).Error; err != nil {
			return err
		}
		if len(entries) > 0 {
			if err := tx.Create(&entries).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListByMetric returns entries for a metric ordered by rank ASC, limited to limit.
func (r *leaderboardRepo) ListByMetric(ctx context.Context, metric string, limit int) ([]models.LeaderboardEntry, error) {
	var entries []models.LeaderboardEntry
	err := r.db.WithContext(ctx).
		Where("metric = ?", metric).
		Order("rank ASC").
		Limit(limit).
		Find(&entries).Error
	return entries, err
}

// GetByUserAndMetric returns the user's entry for a metric, or ErrLeaderboardEntryNotFound if none.
func (r *leaderboardRepo) GetByUserAndMetric(ctx context.Context, metric string, userID uint) (*models.LeaderboardEntry, error) {
	var entry models.LeaderboardEntry
	err := r.db.WithContext(ctx).
		Where("metric = ? AND user_id = ?", metric, userID).
		First(&entry).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrLeaderboardEntryNotFound
		}
		return nil, err
	}
	return &entry, nil
}
