package repository

import (
	"context"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

type portfolioRepo struct {
	db *gorm.DB
}

func NewPortfolioRepository(db *gorm.DB) domain.PortfolioRepository {
	return &portfolioRepo{db: db}
}

// GetUserPositions returns the user's open positions with their stock. The stock is preloaded Unscoped
// so a soft-deleted instrument never drops (or blanks) a position the user still holds.
func (r *portfolioRepo) GetUserPositions(ctx context.Context, userID uint) ([]models.Position, error) {
	var positions []models.Position
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Preload("Stock", func(db *gorm.DB) *gorm.DB { return db.Unscoped() }).
		Find(&positions).Error
	return positions, err
}
