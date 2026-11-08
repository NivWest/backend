package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type aiBriefRepo struct{ db *gorm.DB }

// NewAIBriefRepository returns the GORM-backed instrument brief cache (story NW-14), keyed by the unique
// index idx_ai_briefs_key (orderbook_id, utc_date, prompt_version).
func NewAIBriefRepository(db *gorm.DB) domain.AIBriefRepository { return &aiBriefRepo{db: db} }

func (r *aiBriefRepo) Get(ctx context.Context, orderbookID, utcDate, promptVersion string) (domain.StoredBrief, error) {
	var row models.AIInstrumentBrief
	err := r.db.WithContext(ctx).
		Where("orderbook_id = ? AND utc_date = ? AND prompt_version = ?", orderbookID, utcDate, promptVersion).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.StoredBrief{}, domain.ErrBriefNotFound
	}
	if err != nil {
		return domain.StoredBrief{}, err
	}
	return toStoredBrief(row)
}

// Save inserts with ON CONFLICT DO NOTHING and re-reads the key, so concurrent writers (other API instances)
// all return the first stored brief.
func (r *aiBriefRepo) Save(ctx context.Context, b domain.StoredBrief) (domain.StoredBrief, error) {
	raw, err := json.Marshal(b.Brief)
	if err != nil {
		return domain.StoredBrief{}, fmt.Errorf("marshal brief: %w", err)
	}
	row := models.AIInstrumentBrief{
		OrderbookID:   b.OrderbookID,
		UTCDate:       b.UTCDate,
		PromptVersion: b.PromptVersion,
		Model:         b.Model,
		BriefJSON:     string(raw),
		GeneratedAt:   b.GeneratedAt.UTC(),
	}
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return domain.StoredBrief{}, err
	}
	return r.Get(ctx, b.OrderbookID, b.UTCDate, b.PromptVersion)
}

func toStoredBrief(row models.AIInstrumentBrief) (domain.StoredBrief, error) {
	var brief domain.Brief
	if err := json.Unmarshal([]byte(row.BriefJSON), &brief); err != nil {
		return domain.StoredBrief{}, fmt.Errorf("decode cached brief %d: %w", row.ID, err)
	}
	return domain.StoredBrief{
		OrderbookID:   row.OrderbookID,
		UTCDate:       row.UTCDate,
		PromptVersion: row.PromptVersion,
		Model:         row.Model,
		Brief:         brief,
		GeneratedAt:   row.GeneratedAt.UTC(),
	}, nil
}
