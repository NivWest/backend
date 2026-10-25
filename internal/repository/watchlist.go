package repository

import (
	"context"
	"errors"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

type watchlistRepo struct {
	db *gorm.DB
}

func NewWatchlistRepository(db *gorm.DB) domain.WatchlistRepository {
	return &watchlistRepo{db: db}
}

func (r *watchlistRepo) Create(ctx context.Context, userID uint, name string) (*models.Watchlist, error) {
	watchlist := models.Watchlist{
		UserID: userID,
		Name:   name,
	}
	err := r.db.WithContext(ctx).Create(&watchlist).Error
	return &watchlist, err
}

func (r *watchlistRepo) GetUserWatchlists(ctx context.Context, userID uint) ([]models.Watchlist, error) {
	var watchlists []models.Watchlist
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Preload("Items.Stock").
		Find(&watchlists).Error
	return watchlists, err
}

func (r *watchlistRepo) Delete(ctx context.Context, userID uint, watchlistID uint) error {
	res := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", watchlistID, userID).Delete(&models.Watchlist{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("watchlist not found or not authorized")
	}
	return nil
}

// EnsureStock returns the first stock with this symbol. Watchlists stay keyed by symbol until their
// own story moves them to orderbook ids; a missing stock is created with OrderbookID = symbol, using
// the same race-safe insert-or-reselect as orderRepo.EnsureStock.
func (r *watchlistRepo) EnsureStock(ctx context.Context, symbol, name string) (*models.Stock, error) {
	db := r.db.WithContext(ctx)
	var stock models.Stock
	err := db.Where("symbol = ?", symbol).Order("id").Limit(1).Find(&stock).Error
	if err != nil {
		return nil, err
	}
	if stock.ID != 0 {
		return &stock, nil
	}
	return ensureStockRow(db, symbol, symbol, name)
}

func (r *watchlistRepo) AddItem(ctx context.Context, userID uint, watchlistID uint, stockID uint) error {
	// First verify watchlist belongs to user
	var wl models.Watchlist
	if err := r.db.WithContext(ctx).Select("id").Where("id = ? AND user_id = ?", watchlistID, userID).First(&wl).Error; err != nil {
		return errors.New("watchlist not found or unauthorized")
	}

	item := models.WatchlistItem{
		WatchlistID: watchlistID,
		StockID:     stockID,
	}

	// Ignore if already exists (using FirstOrCreate)
	return r.db.WithContext(ctx).Where("watchlist_id = ? AND stock_id = ?", watchlistID, stockID).FirstOrCreate(&item).Error
}

func (r *watchlistRepo) RemoveItem(ctx context.Context, userID uint, watchlistID uint, symbol string) error {
	var wl models.Watchlist
	if err := r.db.WithContext(ctx).Select("id").Where("id = ? AND user_id = ?", watchlistID, userID).First(&wl).Error; err != nil {
		return errors.New("watchlist not found or unauthorized")
	}

	var stock models.Stock
	if err := r.db.WithContext(ctx).Where("symbol = ?", symbol).First(&stock).Error; err != nil {
		return errors.New("stock not found")
	}

	return r.db.WithContext(ctx).Where("watchlist_id = ? AND stock_id = ?", watchlistID, stock.ID).Delete(&models.WatchlistItem{}).Error
}
