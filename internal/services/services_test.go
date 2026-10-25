package services

import (
	"context"
	"errors"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

func TestWatchlistService_AddItem_EnsuresStockThenAdds(t *testing.T) {
	var added struct{ userID, listID, stockID uint }
	repo := &testutil.WatchlistRepo{
		EnsureStockFn: func(_ context.Context, symbol, name string) (*models.Stock, error) {
			return &models.Stock{ID: 99, Symbol: symbol, Name: name}, nil
		},
		AddItemFn: func(_ context.Context, userID, listID, stockID uint) error {
			added.userID, added.listID, added.stockID = userID, listID, stockID
			return nil
		},
	}

	err := NewWatchlistService(repo).AddItem(context.Background(), 3, 5, domain.WatchlistItemRequest{Symbol: "ABB", Name: "ABB Ltd"})
	if err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	if added.userID != 3 || added.listID != 5 || added.stockID != 99 {
		t.Errorf("AddItem called with %+v, want user 3, list 5, stock 99", added)
	}
}

func TestWatchlistService_AddItem_StopsWhenEnsureStockFails(t *testing.T) {
	ensureErr := errors.New("db down")
	addCalled := false
	repo := &testutil.WatchlistRepo{
		EnsureStockFn: func(context.Context, string, string) (*models.Stock, error) { return nil, ensureErr },
		AddItemFn:     func(context.Context, uint, uint, uint) error { addCalled = true; return nil },
	}

	err := NewWatchlistService(repo).AddItem(context.Background(), 1, 1, domain.WatchlistItemRequest{Symbol: "X"})
	if !errors.Is(err, ensureErr) {
		t.Fatalf("error = %v, want %v", err, ensureErr)
	}
	if addCalled {
		t.Error("AddItem must not be called when EnsureStock fails")
	}
}

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
