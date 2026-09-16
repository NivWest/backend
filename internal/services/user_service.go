package services

import (
	"context"

	"backend/internal/models"
	"backend/internal/domain"
)

type UserService struct {
	repo  domain.UserRepository
}

func NewUserService(repo domain.UserRepository) *UserService {
	return &UserService{repo: repo}
}

func (s *UserService) GetUserByID(ctx context.Context, id uint) (*models.User, error) {

	user, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (s *UserService) CreateUser(ctx context.Context, user *models.User) error {
	// authenticate user logic can be added here if needed
	return s.repo.Create(ctx, user)
}