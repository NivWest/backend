package repository

import (
	"context"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"gorm.io/gorm"
)

type authRepo struct{ db *gorm.DB }

func NewAuthRepository(db *gorm.DB) domain.AuthRepository { return &authRepo{db: db} }

func (r *authRepo) SaveOAuthState(ctx context.Context, state *models.OAuthState) error {
	return r.db.WithContext(ctx).Create(state).Error
}

func (r *authRepo) ConsumeOAuthState(ctx context.Context, state string) (*models.OAuthState, error) {
	var record models.OAuthState
	if err := r.db.WithContext(ctx).Where("state = ? AND expires_at > ?", state, time.Now()).First(&record).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Delete(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *authRepo) CreateSession(ctx context.Context, session *models.Session) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *authRepo) GetSessionUser(ctx context.Context, sessionID string) (*models.User, error) {
	var session models.Session
	if err := r.db.WithContext(ctx).Preload("User").Where("id = ? AND expires_at > ?", sessionID, time.Now()).First(&session).Error; err != nil {
		return nil, err
	}
	return &session.User, nil
}

func (r *authRepo) DeleteSession(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Delete(&models.Session{}, "id = ?", sessionID).Error
}
