package services

import (
	"context"
	"errors"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

func TestUserService_DelegatesToRepository(t *testing.T) {
	repo := testutil.NewUserRepo(&models.User{ID: 1, Email: "a@example.com"})
	svc := NewUserService(repo)
	ctx := context.Background()

	if err := svc.DeleteUser(ctx, 1); err != nil {
		t.Errorf("DeleteUser() error = %v", err)
	}
	if _, ok := repo.Users[1]; ok {
		t.Error("DeleteUser() must remove the user from the repository")
	}
	if err := svc.DeleteUser(ctx, 1); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("DeleteUser(again) error = %v, want ErrUserNotFound", err)
	}

	repoErr := errors.New("db down")
	repo.DeleteFn = func(context.Context, uint) error { return repoErr }
	if err := svc.DeleteUser(ctx, 5); !errors.Is(err, repoErr) {
		t.Errorf("DeleteUser(repo failure) error = %v, want %v", err, repoErr)
	}
}
