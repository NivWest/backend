package repository

import (
	"context"
	"fmt"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

// matchingRepo is the matching engine's order book (story NW-08). Fills and rejects share the lock order and the
// OPEN re-check of Cancel (lockOpenOrder in order_pending.go).
type matchingRepo struct {
	db *gorm.DB
}

func NewMatchingRepository(db *gorm.DB) domain.MatchingRepository {
	return &matchingRepo{db: db}
}

// ListOpen implements domain.MatchingRepository over the partial index idx_orders_open.
func (r *matchingRepo) ListOpen(ctx context.Context) ([]domain.OpenOrder, error) {
	var rows []models.Order
	if err := r.db.WithContext(ctx).
		Preload("Stock", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).
		Where("status = ?", string(domain.OrderStatusOpen)).
		Order("id").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list open orders: %w", err)
	}
	open := make([]domain.OpenOrder, 0, len(rows))
	for _, o := range rows {
		open = append(open, domain.OpenOrder{
			ID:          o.ID,
			UserID:      o.UserID,
			StockID:     o.StockID,
			OrderbookID: o.Stock.OrderbookID,
			Side:        domain.OrderSide(o.Side),
			Type:        domain.OrderType(o.Type),
			Quantity:    o.Quantity,
			LimitPrice:  nullDecimal(o.LimitPrice),
			StopPrice:   nullDecimal(o.StopPrice),
		})
	}
	return open, nil
}

// FillOrder implements domain.MatchingRepository. The reservation is released first, so a BUY's own reserved
// cash counts towards its fill while every other order's reservation stays held back. Any error rolls the whole
// transaction back, the release included.
func (r *matchingRepo) FillOrder(ctx context.Context, p domain.FillOrderParams) (*domain.Order, error) {
	var result *domain.Order
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := lockOpenOrder(tx, p.OrderID, nil)
		if err != nil {
			return err
		}
		if err := releaseReservation(tx, locked); err != nil {
			return err
		}
		o := locked.order
		realizedPnLPtr, err := settleFill(tx, locked.user, locked.position, domain.ExecuteOrderParams{
			UserID:     o.UserID,
			StockID:    o.StockID,
			Side:       domain.OrderSide(o.Side),
			Type:       domain.OrderType(o.Type),
			Quantity:   o.Quantity,
			FillPrice:  p.FillPrice,
			Commission: p.Commission,
			Total:      p.Total,
		})
		if err != nil {
			return err
		}
		filledAt := p.FilledAt.UTC()
		if err := tx.Model(o).Updates(map[string]any{
			"status":       string(domain.OrderStatusFilled),
			"fill_price":   p.FillPrice,
			"commission":   p.Commission,
			"total":        p.Total,
			"realized_pnl": realizedPnLPtr,
			"filled_at":    filledAt,
		}).Error; err != nil {
			return fmt.Errorf("fill order %d: %w", p.OrderID, err)
		}
		result, err = reloadOrder(tx, p.OrderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// RejectOrder implements domain.MatchingRepository.
func (r *matchingRepo) RejectOrder(ctx context.Context, orderID uint, reason string) (*domain.Order, error) {
	var result *domain.Order
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := lockOpenOrder(tx, orderID, nil)
		if err != nil {
			return err
		}
		if err := releaseReservation(tx, locked); err != nil {
			return err
		}
		if err := tx.Model(locked.order).Updates(map[string]any{
			"status":        string(domain.OrderStatusRejected),
			"reject_reason": reason,
		}).Error; err != nil {
			return fmt.Errorf("reject order %d: %w", orderID, err)
		}
		result, err = reloadOrder(tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
