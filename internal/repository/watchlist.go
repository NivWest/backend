package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// watchlistRepo implements domain.WatchlistRepository. Every query is scoped by user_id: a list owned by
// someone else is reported as domain.ErrWatchlistNotFound, never as forbidden.
type watchlistRepo struct {
	db *gorm.DB
}

func NewWatchlistRepository(db *gorm.DB) domain.WatchlistRepository {
	return &watchlistRepo{db: db}
}

// watchlistItemRow is one item joined with its stock.
type watchlistItemRow struct {
	WatchlistID uint
	OrderbookID string
	Symbol      string
	Name        string
	CreatedAt   time.Time
}

func (row watchlistItemRow) toDomain() domain.WatchlistItem {
	return domain.WatchlistItem{
		OrderbookID: row.OrderbookID,
		Symbol:      row.Symbol,
		Name:        row.Name,
		AddedAt:     row.CreatedAt.UTC(),
	}
}

// List implements domain.WatchlistRepository.
func (r *watchlistRepo) List(ctx context.Context, userID uint) ([]domain.Watchlist, error) {
	db := r.db.WithContext(ctx)
	var lists []models.Watchlist
	if err := db.Where("user_id = ?", userID).Order("id").Find(&lists).Error; err != nil {
		return nil, fmt.Errorf("list watchlists: %w", err)
	}
	ids := make([]uint, len(lists))
	for i, l := range lists {
		ids[i] = l.ID
	}
	items, err := loadItems(db, ids)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Watchlist, len(lists))
	for i, l := range lists {
		out[i] = toDomainWatchlist(&l, items[l.ID])
	}
	return out, nil
}

// Get implements domain.WatchlistRepository.
func (r *watchlistRepo) Get(ctx context.Context, userID, watchlistID uint) (*domain.Watchlist, error) {
	db := r.db.WithContext(ctx)
	list, err := findOwnedList(db, userID, watchlistID)
	if err != nil {
		return nil, err
	}
	items, err := loadItems(db, []uint{list.ID})
	if err != nil {
		return nil, err
	}
	wl := toDomainWatchlist(list, items[list.ID])
	return &wl, nil
}

// Create implements domain.WatchlistRepository. Locking the user row serializes the user's creates, so the
// count check holds under concurrency.
func (r *watchlistRepo) Create(ctx context.Context, userID uint, name string, max int) (*domain.Watchlist, error) {
	var created models.Watchlist
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Take(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrUserNotFound
			}
			return fmt.Errorf("lock user: %w", err)
		}
		var count int64
		if err := tx.Model(&models.Watchlist{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return fmt.Errorf("count watchlists: %w", err)
		}
		if count >= int64(max) {
			return domain.ErrWatchlistLimit
		}
		created = models.Watchlist{UserID: userID, Name: name}
		if err := tx.Create(&created).Error; err != nil {
			return fmt.Errorf("insert watchlist: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	wl := toDomainWatchlist(&created, nil)
	return &wl, nil
}

// Rename implements domain.WatchlistRepository.
func (r *watchlistRepo) Rename(ctx context.Context, userID, watchlistID uint, name string) error {
	res := r.db.WithContext(ctx).Model(&models.Watchlist{}).
		Where("id = ? AND user_id = ?", watchlistID, userID).
		Update("name", name)
	if res.Error != nil {
		return fmt.Errorf("rename watchlist: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrWatchlistNotFound
	}
	return nil
}

// Delete implements domain.WatchlistRepository: the items go first, then the list, in one transaction.
func (r *watchlistRepo) Delete(ctx context.Context, userID, watchlistID uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var list models.Watchlist
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
			Where("id = ? AND user_id = ?", watchlistID, userID).Take(&list).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.ErrWatchlistNotFound
		}
		if err != nil {
			return fmt.Errorf("lock watchlist: %w", err)
		}
		if err := tx.Where("watchlist_id = ?", list.ID).Delete(&models.WatchlistItem{}).Error; err != nil {
			return fmt.Errorf("delete watchlist items: %w", err)
		}
		if err := tx.Delete(&models.Watchlist{}, list.ID).Error; err != nil {
			return fmt.Errorf("delete watchlist: %w", err)
		}
		return nil
	})
}

// AddItem implements domain.WatchlistRepository. The insert is ON CONFLICT (watchlist_id, stock_id) DO NOTHING,
// so a duplicate (also a concurrent one) inserts nothing and is reported as ErrDuplicateItem.
func (r *watchlistRepo) AddItem(ctx context.Context, userID, watchlistID, stockID uint) (*domain.WatchlistItem, error) {
	db := r.db.WithContext(ctx)
	if _, err := findOwnedList(db, userID, watchlistID); err != nil {
		return nil, err
	}
	item := models.WatchlistItem{WatchlistID: watchlistID, StockID: stockID}
	res := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "watchlist_id"}, {Name: "stock_id"}},
		DoNothing: true,
	}).Create(&item)
	if res.Error != nil {
		return nil, fmt.Errorf("insert watchlist item: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, domain.ErrDuplicateItem
	}
	var stock models.Stock
	if err := db.Unscoped().Take(&stock, stockID).Error; err != nil {
		return nil, fmt.Errorf("select stock %d: %w", stockID, err)
	}
	return &domain.WatchlistItem{
		OrderbookID: stock.OrderbookID,
		Symbol:      stock.Symbol,
		Name:        stock.Name,
		AddedAt:     item.CreatedAt.UTC(),
	}, nil
}

// RemoveItem implements domain.WatchlistRepository.
func (r *watchlistRepo) RemoveItem(ctx context.Context, userID, watchlistID uint, orderbookID string) error {
	db := r.db.WithContext(ctx)
	if _, err := findOwnedList(db, userID, watchlistID); err != nil {
		return err
	}
	res := db.Where("watchlist_id = ? AND stock_id IN (?)", watchlistID,
		db.Unscoped().Model(&models.Stock{}).Select("id").Where("orderbook_id = ?", orderbookID),
	).Delete(&models.WatchlistItem{})
	if res.Error != nil {
		return fmt.Errorf("delete watchlist item: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrWatchlistItemNotFound
	}
	return nil
}

// EnsureStock implements domain.WatchlistRepository with the same race-safe insert-or-reselect as orders.
func (r *watchlistRepo) EnsureStock(ctx context.Context, orderbookID, symbol, name string) (uint, error) {
	stock, err := ensureStockRow(r.db.WithContext(ctx), orderbookID, symbol, name)
	if err != nil {
		return 0, err
	}
	return stock.ID, nil
}

// findOwnedList returns the user's list with this id, or domain.ErrWatchlistNotFound.
func findOwnedList(db *gorm.DB, userID, watchlistID uint) (*models.Watchlist, error) {
	var list models.Watchlist
	err := db.Where("id = ? AND user_id = ?", watchlistID, userID).Take(&list).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrWatchlistNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select watchlist: %w", err)
	}
	return &list, nil
}

// loadItems returns the items of the given lists, oldest first, grouped by list id.
func loadItems(db *gorm.DB, watchlistIDs []uint) (map[uint][]domain.WatchlistItem, error) {
	out := map[uint][]domain.WatchlistItem{}
	if len(watchlistIDs) == 0 {
		return out, nil
	}
	var rows []watchlistItemRow
	err := db.Table("watchlist_items").
		Select("watchlist_items.watchlist_id, stocks.orderbook_id, stocks.symbol, stocks.name, watchlist_items.created_at").
		Joins("JOIN stocks ON stocks.id = watchlist_items.stock_id").
		Where("watchlist_items.watchlist_id IN ?", watchlistIDs).
		Order("watchlist_items.id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("select watchlist items: %w", err)
	}
	for _, row := range rows {
		out[row.WatchlistID] = append(out[row.WatchlistID], row.toDomain())
	}
	return out, nil
}

func toDomainWatchlist(l *models.Watchlist, items []domain.WatchlistItem) domain.Watchlist {
	if items == nil {
		items = []domain.WatchlistItem{}
	}
	return domain.Watchlist{ID: l.ID, Name: l.Name, Items: items, CreatedAt: l.CreatedAt.UTC()}
}
