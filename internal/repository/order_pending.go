package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Pending LIMIT and STOP orders (story NW-08): placement with a reservation, lookup and cancel. The engine side
// (fill, reject) lives in matching.go. Every transaction here locks the user row, then the position, then the
// order, the same order as Execute and the matching engine, so they serialize and never deadlock.

// PlacePending implements domain.OrderRepository.
func (r *orderRepo) PlacePending(ctx context.Context, p domain.PendingOrderParams) (*domain.Order, bool, error) {
	var (
		result  *domain.Order
		created bool
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := lockUser(tx, p.UserID)
		if err != nil {
			return err
		}
		position, err := lockPosition(tx, p.UserID, p.StockID)
		if err != nil {
			return err
		}

		if p.IdempotencyKey != "" {
			existing, found, err := findOrderByKey(tx, p.UserID, p.IdempotencyKey)
			if err != nil {
				return err
			}
			if found {
				result, created = existing, false
				return nil
			}
		}

		var stock models.Stock
		if err := tx.Unscoped().Take(&stock, p.StockID).Error; err != nil {
			return fmt.Errorf("load stock %d: %w", p.StockID, err)
		}

		// Reserve: cash for a BUY (within buying power), shares for a SELL (within available shares).
		reservedCash := decimal.Zero
		switch p.Side {
		case domain.OrderSideBuy:
			if user.Balance.Sub(user.ReservedCash).LessThan(p.ReservedCash) {
				return domain.ErrInsufficientFunds
			}
			reservedCash = p.ReservedCash
			if err := tx.Model(user).Update("reserved_cash", user.ReservedCash.Add(reservedCash)).Error; err != nil {
				return fmt.Errorf("reserve cash: %w", err)
			}
		case domain.OrderSideSell:
			if position == nil || position.Quantity.Sub(position.ReservedQuantity).LessThan(p.Quantity) {
				return domain.ErrInsufficientShares
			}
			if err := tx.Model(position).Update("reserved_quantity", position.ReservedQuantity.Add(p.Quantity)).Error; err != nil {
				return fmt.Errorf("reserve shares: %w", err)
			}
		default:
			return fmt.Errorf("unsupported order side %q", p.Side)
		}

		now := time.Now().UTC().Truncate(time.Microsecond)
		order := models.Order{
			UserID:       p.UserID,
			StockID:      p.StockID,
			Side:         string(p.Side),
			Type:         string(p.Type),
			Status:       string(domain.OrderStatusOpen),
			Quantity:     p.Quantity,
			FillPrice:    decimal.Zero,
			Commission:   decimal.Zero,
			Total:        decimal.Zero,
			LimitPrice:   toNullDecimal(p.LimitPrice),
			StopPrice:    toNullDecimal(p.StopPrice),
			ReservedCash: reservedCash,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if p.IdempotencyKey != "" {
			key := p.IdempotencyKey
			order.IdempotencyKey = &key
		}
		if err := tx.Omit(clause.Associations).Create(&order).Error; err != nil {
			return fmt.Errorf("insert order: %w", err)
		}

		stored, err := reloadOrder(tx, order.ID)
		if err != nil {
			return err
		}
		result, created = stored, true
		return nil
	})
	if err != nil {
		if p.IdempotencyKey != "" && isIdempotencyKeyViolation(err) {
			existing, findErr := r.FindByIdempotencyKey(ctx, p.UserID, p.IdempotencyKey)
			if findErr != nil {
				return nil, false, findErr
			}
			return existing, false, nil
		}
		return nil, false, err
	}
	return result, created, nil
}

// GetByID implements domain.OrderRepository. Scoped by user: another user's id is ErrOrderNotFound.
func (r *orderRepo) GetByID(ctx context.Context, userID, orderID uint) (*domain.Order, error) {
	var order models.Order
	err := r.db.WithContext(ctx).
		Preload("Stock", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).
		Where("id = ? AND user_id = ?", orderID, userID).
		Take(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrOrderNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order %d: %w", orderID, err)
	}
	return toDomainOrder(&order, &order.Stock), nil
}

// Cancel implements domain.OrderRepository.
func (r *orderRepo) Cancel(ctx context.Context, userID, orderID uint) (*domain.Order, error) {
	var result *domain.Order
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := lockOpenOrder(tx, orderID, &userID)
		if err != nil {
			return err
		}
		if err := releaseReservation(tx, locked); err != nil {
			return err
		}
		if err := tx.Model(locked.order).Updates(map[string]any{
			"status": string(domain.OrderStatusCancelled),
		}).Error; err != nil {
			return fmt.Errorf("cancel order %d: %w", orderID, err)
		}
		result, err = reloadOrder(tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// lockedOrder is an OPEN order with the rows it touches, all locked FOR UPDATE.
type lockedOrder struct {
	user     *models.User
	position *models.Position // nil when the user holds none of the stock
	order    *models.Order
}

// lockOpenOrder locks the user row, the position and then the order (the one lock order of every money
// transaction) and re-checks that the order is still OPEN. With userID set, another user's order is
// ErrOrderNotFound. The first read is unlocked: it only finds which user and stock to lock; user_id and
// stock_id never change, and the status is re-read under the order lock.
func lockOpenOrder(tx *gorm.DB, orderID uint, userID *uint) (*lockedOrder, error) {
	q := tx.Where("id = ?", orderID)
	if userID != nil {
		q = q.Where("user_id = ?", *userID)
	}
	var probe models.Order
	if err := q.Take(&probe).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, fmt.Errorf("find order %d: %w", orderID, err)
	}

	user, err := lockUser(tx, probe.UserID)
	if err != nil {
		return nil, err
	}
	position, err := lockPosition(tx, probe.UserID, probe.StockID)
	if err != nil {
		return nil, err
	}
	var order models.Order
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&order, orderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, fmt.Errorf("lock order %d: %w", orderID, err)
	}
	if order.Status != string(domain.OrderStatusOpen) {
		return nil, domain.ErrOrderNotOpen
	}
	return &lockedOrder{user: user, position: position, order: &order}, nil
}

// releaseReservation gives back what the locked OPEN order reserved: its reserved_cash on the user (BUY) or its
// quantity on the position's reserved_quantity (SELL). The in-memory rows are updated too, so a fill in the same
// transaction sees the released state. Never goes below zero.
func releaseReservation(tx *gorm.DB, l *lockedOrder) error {
	switch domain.OrderSide(l.order.Side) {
	case domain.OrderSideBuy:
		reserved := decimal.Max(decimal.Zero, l.user.ReservedCash.Sub(l.order.ReservedCash))
		if err := tx.Model(l.user).Update("reserved_cash", reserved).Error; err != nil {
			return fmt.Errorf("release cash: %w", err)
		}
		l.user.ReservedCash = reserved
	case domain.OrderSideSell:
		if l.position == nil {
			return nil
		}
		reserved := decimal.Max(decimal.Zero, l.position.ReservedQuantity.Sub(l.order.Quantity))
		if err := tx.Model(l.position).Update("reserved_quantity", reserved).Error; err != nil {
			return fmt.Errorf("release shares: %w", err)
		}
		l.position.ReservedQuantity = reserved
	}
	return nil
}

// reloadOrder re-reads a stored order with its stock and maps it, so a response is exactly what a later read
// returns (timestamp precision, decimal scale).
func reloadOrder(tx *gorm.DB, orderID uint) (*domain.Order, error) {
	var stored models.Order
	if err := tx.Preload("Stock", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).Take(&stored, orderID).Error; err != nil {
		return nil, fmt.Errorf("reload order %d: %w", orderID, err)
	}
	return toDomainOrder(&stored, &stored.Stock), nil
}
