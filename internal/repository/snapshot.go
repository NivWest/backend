package repository

import (
	"context"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

// snapshotBatchSize bounds the rows of one INSERT (7 columns each, far below Postgres' bind-parameter limit).
const snapshotBatchSize = 500

type snapshotRepo struct {
	db *gorm.DB
}

// NewSnapshotRepository returns the GORM implementation of domain.SnapshotRepository (story NW-10).
func NewSnapshotRepository(db *gorm.DB) domain.SnapshotRepository {
	return &snapshotRepo{db: db}
}

// InsertBatch implements domain.SnapshotRepository with CreateInBatches.
func (r *snapshotRepo) InsertBatch(ctx context.Context, snapshots []models.EquitySnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(&snapshots, snapshotBatchSize).Error
}

// ListByUser implements domain.SnapshotRepository over idx_equity_snapshots_user_t.
func (r *snapshotRepo) ListByUser(ctx context.Context, userID uint, since time.Time) ([]models.EquitySnapshot, error) {
	snapshots := []models.EquitySnapshot{}
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if !since.IsZero() {
		q = q.Where("t >= ?", since)
	}
	if err := q.Order("t ASC, id ASC").Find(&snapshots).Error; err != nil {
		return nil, err
	}
	return snapshots, nil
}

// UserIDsWithOrders implements domain.SnapshotRepository; soft-deleted users are left out.
func (r *snapshotRepo) UserIDsWithOrders(ctx context.Context) ([]uint, error) {
	ids := []uint{}
	err := r.db.WithContext(ctx).
		Raw(`SELECT DISTINCT o.user_id FROM orders o
			JOIN users u ON u.id = o.user_id AND u.deleted_at IS NULL
			ORDER BY o.user_id`).
		Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}
