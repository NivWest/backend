package domain

import (
	"context"
	"time"

	"backend/internal/models"
)

type UserRepository interface {
	Create(ctx context.Context, user *models.User) error
	UpsertGoogle(ctx context.Context, user *models.User) (*models.User, error)
	GetByID(ctx context.Context, id uint) (*models.User, error)
	GetByDisplayName(ctx context.Context, displayName string) (*models.User, error)
	UpdateProfile(ctx context.Context, userID uint, displayName *string, optIn *bool, optedInAt *time.Time) (*models.User, error)
	ListOptedInUsers(ctx context.Context) ([]models.User, error)
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
