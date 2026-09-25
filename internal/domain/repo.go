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

type OrderRequest struct {
	Symbol   string  `json:"symbol" binding:"required"`
	Name     string  `json:"name"` // Used if stock doesn't exist in DB yet
	Type     string  `json:"type" binding:"required,oneof=BUY SELL"` // BUY, SELL
	Quantity float64 `json:"quantity" binding:"required,gt=0"`
}

type OrderRepository interface {
	// ExecuteTransaction handles the DB transaction: updates user balance, updates/creates position, creates transaction record
	ExecuteTransaction(ctx context.Context, userID uint, stockID uint, orderType string, quantity float64, price float64, total float64) error
	GetPosition(ctx context.Context, userID uint, stockID uint) (*models.Position, error)
	EnsureStock(ctx context.Context, symbol, name string) (*models.Stock, error)
}

type WatchlistRepository interface {
	Create(ctx context.Context, userID uint, name string) (*models.Watchlist, error)
	GetUserWatchlists(ctx context.Context, userID uint) ([]models.Watchlist, error)
	Delete(ctx context.Context, userID uint, watchlistID uint) error
	
	AddItem(ctx context.Context, userID uint, watchlistID uint, stockID uint) error
	RemoveItem(ctx context.Context, userID uint, watchlistID uint, stockID uint) error
	
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
