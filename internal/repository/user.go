package repository

import (
    "context"
    "errors"

    "backend/internal/domain"
    "backend/internal/models"
    "gorm.io/gorm"
)

type userRepo struct {
    db *gorm.DB
}

func NewUserRepository(db *gorm.DB) domain.UserRepository {
    return &userRepo{db: db}
}

func (r *userRepo) Create(ctx context.Context, user *models.User) error {
    var existing models.User

    err := r.db.WithContext(ctx).
        Where("google_id = ? OR email = ?", user.GoogleID, user.Email).
        First(&existing).Error

    if err == nil {
        return domain.ErrUserAlreadyExists
    }

    if !errors.Is(err, gorm.ErrRecordNotFound) {
        return err
    }

    return r.db.WithContext(ctx).Create(user).Error
}

func (r *userRepo) GetByID(ctx context.Context, id uint) (*models.User, error) {
    var user models.User

    err := r.db.WithContext(ctx).First(&user, id).Error
    if errors.Is(err, gorm.ErrRecordNotFound) {
        return nil, domain.ErrUserNotFound
    }

    if err != nil {
        return nil, err
    }

    return &user, nil
}