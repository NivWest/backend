package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// pgUniqueViolation is the Postgres SQLSTATE for a unique constraint violation.
const pgUniqueViolation = "23505"

// orderIdempotencyIndex is the unique index on orders (user_id, idempotency_key), see models.Order.
const orderIdempotencyIndex = "idx_orders_user_idempotency_key"

type orderRepo struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) domain.OrderRepository {
	return &orderRepo{db: db}
}

// EnsureStock implements domain.OrderRepository. The insert is ON CONFLICT (orderbook_id) DO NOTHING,
// so concurrent first orders for one instrument never fail on the unique index; the reselect then
// returns whichever row won. It is Unscoped so a soft-deleted stock row is reused, not duplicated.
func (r *orderRepo) EnsureStock(ctx context.Context, orderbookID, symbol, name string) (uint, error) {
	stock, err := ensureStockRow(r.db.WithContext(ctx), orderbookID, symbol, name)
	if err != nil {
		return 0, err
	}
	return stock.ID, nil
}

// ensureStockRow inserts the stock if missing and returns the row stored for orderbookID.
func ensureStockRow(db *gorm.DB, orderbookID, symbol, name string) (*models.Stock, error) {
	candidate := models.Stock{OrderbookID: orderbookID, Symbol: symbol, Name: name}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "orderbook_id"}},
		DoNothing: true,
	}).Create(&candidate).Error; err != nil {
		return nil, fmt.Errorf("insert stock %q: %w", orderbookID, err)
	}

	var stock models.Stock
	if err := db.Unscoped().Where("orderbook_id = ?", orderbookID).Take(&stock).Error; err != nil {
		return nil, fmt.Errorf("select stock %q: %w", orderbookID, err)
	}
	return &stock, nil
}

// FindByIdempotencyKey implements domain.OrderRepository.
func (r *orderRepo) FindByIdempotencyKey(ctx context.Context, userID uint, key string) (*domain.Order, error) {
	order, found, err := findOrderByKey(r.db.WithContext(ctx), userID, key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, domain.ErrOrderNotFound
	}
	return order, nil
}

// findOrderByKey looks the user's order with key up; found is false when there is none.
func findOrderByKey(db *gorm.DB, userID uint, key string) (*domain.Order, bool, error) {
	var order models.Order
	err := db.
		Preload("Stock", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).
		Where("user_id = ? AND idempotency_key = ?", userID, key).
		Take(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find order by idempotency key: %w", err)
	}
	return toDomainOrder(&order, &order.Stock), true, nil
}

// Execute implements domain.OrderRepository. Lock order is always user row -> position row, so two
// orders of one user serialize on the user lock and can never deadlock each other. Because the
// idempotency check runs after the user lock, a concurrent retry with the same key waits for the
// first order to commit and then finds it.
func (r *orderRepo) Execute(ctx context.Context, p domain.ExecuteOrderParams) (*domain.Order, bool, error) {
	var (
		result  *domain.Order
		created bool
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// (a) Lock the user row: serializes every order of this user.
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&user, p.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrUserNotFound
			}
			return fmt.Errorf("lock user: %w", err)
		}

		// (b) Lock the position for this instrument (it may not exist yet).
		var positions []models.Position
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND stock_id = ?", p.UserID, p.StockID).
			Limit(1).Find(&positions).Error; err != nil {
			return fmt.Errorf("lock position: %w", err)
		}
		var position *models.Position
		if len(positions) > 0 {
			position = &positions[0]
		}

		// (c) Idempotency: an order with this key already exists -> return it, change nothing.
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

		// (d) Check sufficiency and move cash and shares.
		balance := user.Balance
		switch p.Side {
		case domain.OrderSideBuy:
			if balance.LessThan(p.Total) {
				return domain.ErrInsufficientFunds
			}
			balance = balance.Sub(p.Total)
			if err := applyBuy(tx, position, p); err != nil {
				return err
			}
		case domain.OrderSideSell:
			if position == nil || position.Quantity.LessThan(p.Quantity) {
				return domain.ErrInsufficientShares
			}
			balance = balance.Add(p.Total)
			if err := applySell(tx, position, p.Quantity); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported order side %q", p.Side)
		}

		// (e) Only the balance column changes on the user row.
		if err := tx.Model(&user).Update("balance", balance).Error; err != nil {
			return fmt.Errorf("update balance: %w", err)
		}

		// (f) Record the filled order. Postgres timestamptz keeps microseconds, so truncate now:
		// the order returned here must equal what a later read (an idempotent replay) returns.
		now := time.Now().UTC().Truncate(time.Microsecond)
		orderType := p.Type
		if orderType == "" {
			orderType = domain.OrderTypeMarket
		}
		order := models.Order{
			UserID:     p.UserID,
			StockID:    p.StockID,
			Side:       string(p.Side),
			Type:       string(orderType),
			Status:     string(domain.OrderStatusFilled),
			Quantity:   p.Quantity,
			FillPrice:  p.FillPrice,
			Commission: p.Commission,
			Total:      p.Total,
			CreatedAt:  now,
			UpdatedAt:  now,
			FilledAt:   &now,
		}
		if p.IdempotencyKey != "" {
			key := p.IdempotencyKey
			order.IdempotencyKey = &key
		}
		if err := tx.Omit(clause.Associations).Create(&order).Error; err != nil {
			return fmt.Errorf("insert order: %w", err)
		}

		// (g) Re-read the stored row and map it, so the created response is exactly what a later read
		// (an idempotent replay) returns: same timestamp precision and same decimal scale.
		var stored models.Order
		if err := tx.Take(&stored, order.ID).Error; err != nil {
			return fmt.Errorf("reload order %d: %w", order.ID, err)
		}
		result, created = toDomainOrder(&stored, &stock), true
		return nil
	})
	if err != nil {
		if p.IdempotencyKey != "" && isIdempotencyKeyViolation(err) {
			// Another transaction stored this key first; ours rolled back, so answer with its order.
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

// applyBuy adds the bought shares to the (locked) position, creating it when missing. The new
// average price is the quantity-weighted average of the old average and the fill price.
func applyBuy(tx *gorm.DB, position *models.Position, p domain.ExecuteOrderParams) error {
	if position == nil {
		created := models.Position{
			UserID:   p.UserID,
			StockID:  p.StockID,
			Quantity: p.Quantity,
			AvgPrice: p.FillPrice.Round(2),
		}
		if err := tx.Omit(clause.Associations).Create(&created).Error; err != nil {
			return fmt.Errorf("create position: %w", err)
		}
		return nil
	}

	newQty := position.Quantity.Add(p.Quantity)
	cost := position.Quantity.Mul(position.AvgPrice).Add(p.Quantity.Mul(p.FillPrice))
	newAvg := cost.Div(newQty).Round(2)
	if err := tx.Model(position).Updates(map[string]any{
		"quantity":  newQty,
		"avg_price": newAvg,
	}).Error; err != nil {
		return fmt.Errorf("update position: %w", err)
	}
	return nil
}

// applySell removes the sold shares from the (locked) position; the average price is unchanged and
// the row is deleted once nothing is held.
func applySell(tx *gorm.DB, position *models.Position, quantity decimal.Decimal) error {
	remaining := position.Quantity.Sub(quantity)
	if remaining.IsZero() {
		if err := tx.Delete(position).Error; err != nil {
			return fmt.Errorf("delete position: %w", err)
		}
		return nil
	}
	if err := tx.Model(position).Update("quantity", remaining).Error; err != nil {
		return fmt.Errorf("update position: %w", err)
	}
	return nil
}

// isIdempotencyKeyViolation reports whether err is a unique violation of (user_id, idempotency_key).
func isIdempotencyKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == orderIdempotencyIndex
}

// toDomainOrder maps a stored order; timestamps are normalized to UTC so a freshly created order and
// the same order read back from Postgres (which scans timestamptz in the connection's location) are equal.
func toDomainOrder(o *models.Order, s *models.Stock) *domain.Order {
	var filledAt *time.Time
	if o.FilledAt != nil {
		t := o.FilledAt.UTC()
		filledAt = &t
	}
	return &domain.Order{
		ID:          o.ID,
		OrderbookID: s.OrderbookID,
		Symbol:      s.Symbol,
		Name:        s.Name,
		Side:        domain.OrderSide(o.Side),
		Type:        domain.OrderType(o.Type),
		Status:      domain.OrderStatus(o.Status),
		Quantity:    o.Quantity,
		FillPrice:   o.FillPrice,
		Commission:  o.Commission,
		Total:       o.Total,
		CreatedAt:   o.CreatedAt.UTC(),
		FilledAt:    filledAt,
	}
}
