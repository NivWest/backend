package repository

import (
	"context"
	"errors"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type orderRepo struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) domain.OrderRepository {
	return &orderRepo{db: db}
}

func (r *orderRepo) EnsureStock(ctx context.Context, symbol, name string) (*models.Stock, error) {
	var stock models.Stock
	err := r.db.WithContext(ctx).Where("symbol = ?", symbol).First(&stock).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		stock = models.Stock{
			Symbol: symbol,
			Name:   name,
		}
		if err := r.db.WithContext(ctx).Create(&stock).Error; err != nil {
			return nil, err
		}
		return &stock, nil
	}
	return &stock, err
}

func (r *orderRepo) GetPosition(ctx context.Context, userID uint, stockID uint) (*models.Position, error) {
	var position models.Position
	err := r.db.WithContext(ctx).Where("user_id = ? AND stock_id = ?", userID, stockID).First(&position).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil // Return nil if no position exists
	}
	return &position, err
}

func (r *orderRepo) ExecuteTransaction(
	ctx context.Context,
	userID uint,
	stockID uint,
	orderType string,
	quantity float64,
	price float64,
	total float64,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}

		var position models.Position
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND stock_id = ?", userID, stockID).First(&position).Error
		posExists := true
		if errors.Is(err, gorm.ErrRecordNotFound) {
			posExists = false
			position = models.Position{
				UserID:   userID,
				StockID:  stockID,
				Quantity: 0,
				AvgPrice: 0,
			}
		} else if err != nil {
			return err
		}

		if orderType == "BUY" {
			if user.Balance < total {
				return errors.New("insufficient funds")
			}
			user.Balance -= total

			// Calculate new average price
			totalValue := (position.Quantity * position.AvgPrice) + (quantity * price)
			position.Quantity += quantity
			position.AvgPrice = totalValue / position.Quantity

		} else if orderType == "SELL" {
			if !posExists || position.Quantity < quantity {
				return errors.New("insufficient shares to sell")
			}
			user.Balance += total
			position.Quantity -= quantity
			// AvgPrice stays the same when selling
		} else {
			return errors.New("invalid order type")
		}

		if err := tx.Save(&user).Error; err != nil {
			return err
		}

		if position.Quantity == 0 {
			if posExists {
				if err := tx.Delete(&position).Error; err != nil {
					return err
				}
			}
		} else {
			if err := tx.Save(&position).Error; err != nil {
				return err
			}
		}

		transaction := models.Transaction{
			UserID:   userID,
			StockID:  stockID,
			Type:     orderType,
			Quantity: quantity,
			Price:    price,
			Total:    total,
		}

		if err := tx.Create(&transaction).Error; err != nil {
			return err
		}

		return nil
	})
}
