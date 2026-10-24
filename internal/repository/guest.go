package repository

import (
	"context"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

// guestIDBatchSize bounds the number of ids bound into one IN (...) list, well below
// Postgres' 65535 bind-parameter limit.
const guestIDBatchSize = 1000

type guestRepo struct{ db *gorm.DB }

func NewGuestRepository(db *gorm.DB) domain.GuestRepository { return &guestRepo{db: db} }

// DeleteExpiredGuests implements domain.GuestRepository. Everything runs in one transaction;
// rows are hard-deleted children first so foreign keys never block the user delete.
func (r *guestRepo) DeleteExpiredGuests(ctx context.Context, cutoff time.Time) (int64, error) {
	var deleted int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Unscoped().Model(&models.User{}).
			Where("role = ? AND created_at < ?", domain.RoleGuest, cutoff).
			Where("NOT EXISTS (SELECT 1 FROM sessions s WHERE s.user_id = users.id AND s.expires_at >= ?)", cutoff).
			Pluck("id", &ids).Error; err != nil {
			return err
		}

		for start := 0; start < len(ids); start += guestIDBatchSize {
			batch := ids[start:min(start+guestIDBatchSize, len(ids))]
			n, err := deleteGuestBatch(tx, batch)
			if err != nil {
				return err
			}
			deleted += n
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

// deleteGuestBatch hard-deletes the given users and all their dependent rows, returning
// the number of users removed.
func deleteGuestBatch(tx *gorm.DB, ids []uint) (int64, error) {
	children := []struct {
		model any
		where string
	}{
		{&models.WatchlistItem{}, "watchlist_id IN (SELECT id FROM watchlists WHERE user_id IN ?)"},
		{&models.Watchlist{}, "user_id IN ?"},
		{&models.Position{}, "user_id IN ?"},
		{&models.Transaction{}, "user_id IN ?"},
		{&models.Session{}, "user_id IN ?"},
	}
	for _, c := range children {
		if err := tx.Unscoped().Where(c.where, ids).Delete(c.model).Error; err != nil {
			return 0, err
		}
	}

	if err := tx.Exec("DELETE FROM user_favorites WHERE user_id IN ?", ids).Error; err != nil {
		return 0, err
	}

	res := tx.Unscoped().Where("id IN ?", ids).Delete(&models.User{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}
