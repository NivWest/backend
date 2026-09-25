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

func (r *portfolioRepo) GetUserPositions(ctx context.Context, userID uint) ([]models.Position, error) {
	var positions []models.Position
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Preload("Stock").
		Find(&positions).Error
	return positions, err
}
