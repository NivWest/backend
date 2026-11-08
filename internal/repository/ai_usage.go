package repository

import (
	"context"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

type aiUsageRepo struct{ db *gorm.DB }

// NewAIUsageRepository returns the GORM-backed LLM usage ledger (story NW-14).
func NewAIUsageRepository(db *gorm.DB) domain.AIUsageRepository { return &aiUsageRepo{db: db} }

func (r *aiUsageRepo) Record(ctx context.Context, rec domain.AIUsageRecord) error {
	row := models.AIUsage{
		UserID:           rec.UserID,
		Feature:          string(rec.Feature),
		Model:            rec.Model,
		PromptVersion:    rec.PromptVersion,
		InputTokens:      rec.Usage.Input,
		OutputTokens:     rec.Usage.Output,
		CacheReadTokens:  rec.Usage.CacheRead,
		CacheWriteTokens: rec.Usage.CacheWrite,
		LatencyMs:        rec.LatencyMs,
		Status:           rec.Status,
		FallbackModel:    rec.FallbackModel,
		CostUSD:          rec.CostUSD,
		CreatedAt:        rec.CreatedAt,
	}
	return r.db.WithContext(ctx).Create(&row).Error
}

// DailyByFeature sums the user's ledger rows in [from, to) per feature over idx_ai_usages_user_created.
func (r *aiUsageRepo) DailyByFeature(ctx context.Context, userID uint, from, to time.Time) ([]domain.AIFeatureUsage, error) {
	var rows []struct {
		Feature          string
		Calls            int64
		InputTokens      int64
		OutputTokens     int64
		CacheReadTokens  int64
		CacheWriteTokens int64
	}
	err := r.db.WithContext(ctx).Model(&models.AIUsage{}).
		Select("feature, COUNT(*) AS calls, COALESCE(SUM(input_tokens), 0) AS input_tokens, "+
			"COALESCE(SUM(output_tokens), 0) AS output_tokens, COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, "+
			"COALESCE(SUM(cache_write_tokens), 0) AS cache_write_tokens").
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, from, to).
		Group("feature").Order("feature").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.AIFeatureUsage, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.AIFeatureUsage{
			Feature:          domain.AIFeature(row.Feature),
			Calls:            row.Calls,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
		})
	}
	return out, nil
}
