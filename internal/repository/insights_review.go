package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

type insightsReviewRepo struct {
	db *gorm.DB
}

// NewInsightsReviewRepository returns the GORM-backed repository for AI behavioral reviews (story NW-17).
func NewInsightsReviewRepository(db *gorm.DB) domain.InsightsReviewRepository {
	return &insightsReviewRepo{db: db}
}

func (r *insightsReviewRepo) Create(ctx context.Context, review *domain.InsightsReview, userID uint, model, promptVersion string) error {
	obs := review.Observations
	if obs == nil {
		obs = []domain.ReviewObservation{}
	}
	raw, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("marshal observations: %w", err)
	}
	row := models.AIInsightReview{
		UserID:           userID,
		Headline:         review.Headline,
		ObservationsJSON: string(raw),
		HabitToTry:       review.HabitToTry,
		Disclaimer:       review.Disclaimer,
		Model:            model,
		PromptVersion:    promptVersion,
		GeneratedAt:      review.GeneratedAt.UTC(),
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("create insight review: %w", err)
	}
	review.ID = row.ID
	review.GeneratedAt = row.GeneratedAt.UTC()
	return nil
}

func (r *insightsReviewRepo) List(ctx context.Context, userID uint, limit int) ([]domain.InsightsReview, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []models.AIInsightReview
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("generated_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list insight reviews: %w", err)
	}
	out := make([]domain.InsightsReview, 0, len(rows))
	for _, row := range rows {
		var obs []domain.ReviewObservation
		if err := json.Unmarshal([]byte(row.ObservationsJSON), &obs); err != nil {
			return nil, fmt.Errorf("decode review %d observations: %w", row.ID, err)
		}
		if obs == nil {
			obs = []domain.ReviewObservation{}
		}
		for i := range obs {
			if obs[i].OrderIDs == nil {
				obs[i].OrderIDs = []uint{}
			}
		}
		out = append(out, domain.InsightsReview{
			ID:           row.ID,
			GeneratedAt:  row.GeneratedAt.UTC(),
			Headline:     row.Headline,
			Observations: obs,
			HabitToTry:   row.HabitToTry,
			Disclaimer:   row.Disclaimer,
		})
	}
	return out, nil
}
