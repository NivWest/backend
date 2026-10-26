package repository

import (
	"context"
	"fmt"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

// ListByUser implements domain.OrderRepository (story NW-05) as a keyset query over
// idx_orders_user_created_id (user_id, created_at, id): newest first, and with before set only the rows
// strictly after it in that order, i.e. (created_at, id) < (before.CreatedAt, before.ID). The stock is
// preloaded Unscoped so orders of a soft-deleted instrument keep their symbol and name.
func (r *orderRepo) ListByUser(ctx context.Context, userID uint, before *domain.OrderCursor, limit int) ([]domain.Order, error) {
	items := []domain.Order{}
	if limit <= 0 {
		return items, nil
	}

	q := r.db.WithContext(ctx).
		Preload("Stock", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).
		Where("user_id = ?", userID)
	if before != nil {
		q = q.Where("(created_at, id) < (?, ?)", before.CreatedAt, before.ID)
	}
	var rows []models.Order
	if err := q.Order("created_at DESC").Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}

	for i := range rows {
		items = append(items, *toDomainOrder(&rows[i], &rows[i].Stock))
	}
	return items, nil
}
