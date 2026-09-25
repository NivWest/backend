package services

import (
	"context"
	"backend/internal/domain"
	"backend/internal/models"
)

type WatchlistService struct {
	repo domain.WatchlistRepository
}

func NewWatchlistService(repo domain.WatchlistRepository) *WatchlistService {
	return &WatchlistService{repo: repo}
}

func (s *WatchlistService) CreateWatchlist(ctx context.Context, userID uint, name string) (*models.Watchlist, error) {
	return s.repo.Create(ctx, userID, name)
}

func (s *WatchlistService) GetUserWatchlists(ctx context.Context, userID uint) ([]models.Watchlist, error) {
	return s.repo.GetUserWatchlists(ctx, userID)
}

func (s *WatchlistService) DeleteWatchlist(ctx context.Context, userID uint, watchlistID uint) error {
	return s.repo.Delete(ctx, userID, watchlistID)
}

func (s *WatchlistService) AddItem(ctx context.Context, userID uint, watchlistID uint, req domain.WatchlistItemRequest) error {
	stock, err := s.repo.EnsureStock(ctx, req.Symbol, req.Name)
	if err != nil {
		return err
	}
	return s.repo.AddItem(ctx, userID, watchlistID, stock.ID)
}

func (s *WatchlistService) RemoveItem(ctx context.Context, userID uint, watchlistID uint, stockID uint) error {
	return s.repo.RemoveItem(ctx, userID, watchlistID, stockID)
}
