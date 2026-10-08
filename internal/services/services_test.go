package services

import (
	"context"
	"errors"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

func TestStockService_ValidatesInput(t *testing.T) {
	svc := NewStockService(&testutil.StockProvider{})
	ctx := context.Background()

	cases := map[string]func() error{
		"search blank query": func() error { _, err := svc.Search(ctx, "   "); return err },
		"chart empty id":     func() error { _, err := svc.GetPriceChart(ctx, "", "one_month"); return err },
		"details empty id":   func() error { _, err := svc.GetStockDetails(ctx, ""); return err },
		"quote empty id":     func() error { _, err := svc.GetStockQuote(ctx, ""); return err },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestStockService_Search_TrimsQueryAndDelegates(t *testing.T) {
	var gotQuery string
	svc := NewStockService(&testutil.StockProvider{SearchFn: func(_ context.Context, q string) ([]byte, error) {
		gotQuery = q
		return []byte(`[]`), nil
	}})

	out, err := svc.Search(context.Background(), "  volvo ")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotQuery != "volvo" {
		t.Errorf("provider query = %q, want %q", gotQuery, "volvo")
	}
	if string(out) != `[]` {
		t.Errorf("Search() = %s, want []", out)
	}
}

func TestStockService_PropagatesProviderErrors(t *testing.T) {
	providerErr := errors.New("upstream down")
	svc := NewStockService(&testutil.StockProvider{
		GetStockQuoteFn: func(context.Context, string) ([]byte, error) { return nil, providerErr },
	})
	if _, err := svc.GetStockQuote(context.Background(), "123"); !errors.Is(err, providerErr) {
		t.Fatalf("error = %v, want %v", err, providerErr)
	}
}

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

	if u, err := svc.GetUserByID(ctx, 1); err != nil || u.Email != "a@example.com" {
		t.Fatalf("GetUserByID() = %+v, %v", u, err)
	}
	if _, err := svc.GetUserByID(ctx, 2); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("GetUserByID(missing) error = %v, want ErrUserNotFound", err)
	}
	if err := svc.CreateUser(ctx, &models.User{Email: "a@example.com"}); !errors.Is(err, domain.ErrUserAlreadyExists) {
		t.Errorf("CreateUser(duplicate) error = %v, want ErrUserAlreadyExists", err)
	}
	if err := svc.DeleteUser(ctx, 1); err != nil {
		t.Errorf("DeleteUser() error = %v", err)
	}
	if err := svc.DeleteUser(ctx, 1); !errors.Is(err, domain.ErrUserNotFound) {
		t.Errorf("DeleteUser(again) error = %v, want ErrUserNotFound", err)
	}
}

