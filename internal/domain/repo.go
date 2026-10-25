package domain

import (
	"backend/internal/models"
	"context"
)

type WatchlistRequest struct {
	Name string `json:"name" binding:"required"`
}

type WatchlistItemRequest struct {
	Symbol string `json:"symbol" binding:"required"`
	Name   string `json:"name"` // Fallback name
}

type WatchlistRepository interface {
	Create(ctx context.Context, userID uint, name string) (*models.Watchlist, error)
	GetUserWatchlists(ctx context.Context, userID uint) ([]models.Watchlist, error)
	Delete(ctx context.Context, userID uint, watchlistID uint) error

	AddItem(ctx context.Context, userID uint, watchlistID uint, stockID uint) error
	RemoveItem(ctx context.Context, userID uint, watchlistID uint, symbol string) error

	EnsureStock(ctx context.Context, symbol, name string) (*models.Stock, error)
}

type UserRepository interface {
	Create(ctx context.Context, user *models.User) error
	UpsertGoogle(ctx context.Context, user *models.User) (*models.User, error)
	GetByID(ctx context.Context, id uint) (*models.User, error)
	Delete(ctx context.Context, id uint) error
}

type AuthRepository interface {
	SaveOAuthState(ctx context.Context, state *models.OAuthState) error
	ConsumeOAuthState(ctx context.Context, state string) (*models.OAuthState, error)
	CreateSession(ctx context.Context, session *models.Session) error
	GetSessionUser(ctx context.Context, sessionID string) (*models.User, error)
	DeleteSession(ctx context.Context, sessionID string) error
}

type PortfolioRepository interface {
	GetUserPositions(ctx context.Context, userID uint) ([]models.Position, error)
}
