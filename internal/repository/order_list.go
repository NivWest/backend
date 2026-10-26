package repository

import (
	"context"
	"errors"

	"backend/internal/domain"
)

// ListByUser implements domain.OrderRepository (story NW-05). Stub from the contract commit; task
// be-ledger-repo replaces it with the keyset query.
func (r *orderRepo) ListByUser(ctx context.Context, userID uint, before *domain.OrderCursor, limit int) ([]domain.Order, error) {
	return nil, errors.New("order list: not implemented")
}
