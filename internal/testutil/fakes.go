// Package testutil provides in-memory fakes of the domain interfaces for unit tests.
//
// Every fake exposes optional function fields (e.g. GetByIDFn). When a field is nil,
// the fake falls back to a simple, deterministic default so tests only override the
// behaviour they care about. This package must only be imported from *_test.go files.
package testutil

import (
	"context"
	"errors"
	"sync"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
)

// ErrNotConfigured is returned by fakes whose default behaviour is to fail.
var ErrNotConfigured = errors.New("testutil: behaviour not configured")

// Compile-time checks that the fakes satisfy the domain contracts.
var (
	_ domain.UserRepository      = (*UserRepo)(nil)
	_ domain.AuthRepository      = (*AuthRepo)(nil)
	_ domain.OrderRepository     = (*OrderRepo)(nil)
	_ domain.WatchlistRepository = (*WatchlistRepo)(nil)
	_ domain.PortfolioRepository = (*PortfolioRepo)(nil)
)

// ---------------------------------------------------------------------------
// UserRepository
// ---------------------------------------------------------------------------

// UserRepo is a fake domain.UserRepository backed by an in-memory map.
type UserRepo struct {
	mu    sync.Mutex
	Users map[uint]*models.User

	CreateFn       func(ctx context.Context, user *models.User) error
	UpsertGoogleFn func(ctx context.Context, user *models.User) (*models.User, error)
	GetByIDFn      func(ctx context.Context, id uint) (*models.User, error)
	DeleteFn       func(ctx context.Context, id uint) error
}

// NewUserRepo returns a UserRepo seeded with the given users (keyed by ID).
func NewUserRepo(users ...*models.User) *UserRepo {
	r := &UserRepo{Users: map[uint]*models.User{}}
	for _, u := range users {
		r.Users[u.ID] = u
	}
	return r
}

func (r *UserRepo) Create(ctx context.Context, user *models.User) error {
	if r.CreateFn != nil {
		return r.CreateFn(ctx, user)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Users == nil {
		r.Users = map[uint]*models.User{}
	}
	for _, u := range r.Users {
		if u.Email == user.Email {
			return domain.ErrUserAlreadyExists
		}
	}
	if user.ID == 0 {
		user.ID = uint(len(r.Users) + 1)
	}
	r.Users[user.ID] = user
	return nil
}

func (r *UserRepo) UpsertGoogle(ctx context.Context, user *models.User) (*models.User, error) {
	if r.UpsertGoogleFn != nil {
		return r.UpsertGoogleFn(ctx, user)
	}
	return user, nil
}

func (r *UserRepo) GetByID(ctx context.Context, id uint) (*models.User, error) {
	if r.GetByIDFn != nil {
		return r.GetByIDFn(ctx, id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.Users[id]; ok {
		return u, nil
	}
	return nil, domain.ErrUserNotFound
}

func (r *UserRepo) Delete(ctx context.Context, id uint) error {
	if r.DeleteFn != nil {
		return r.DeleteFn(ctx, id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.Users[id]; !ok {
		return domain.ErrUserNotFound
	}
	delete(r.Users, id)
	return nil
}

// ---------------------------------------------------------------------------
// AuthRepository
// ---------------------------------------------------------------------------

// AuthRepo is a fake domain.AuthRepository. Sessions maps a session ID to its user.
type AuthRepo struct {
	mu       sync.Mutex
	Sessions map[string]*models.User
	States   map[string]*models.OAuthState
}

// NewAuthRepo returns an AuthRepo with no sessions.
func NewAuthRepo() *AuthRepo {
	return &AuthRepo{Sessions: map[string]*models.User{}, States: map[string]*models.OAuthState{}}
}

// WithSession registers sessionID for user and returns the repo for chaining.
func (r *AuthRepo) WithSession(sessionID string, user *models.User) *AuthRepo {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Sessions[sessionID] = user
	return r
}

func (r *AuthRepo) SaveOAuthState(_ context.Context, state *models.OAuthState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.States[state.State] = state
	return nil
}

func (r *AuthRepo) ConsumeOAuthState(_ context.Context, state string) (*models.OAuthState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.States[state]
	if !ok {
		return nil, errors.New("oauth state not found")
	}
	delete(r.States, state)
	return s, nil
}

func (r *AuthRepo) CreateSession(_ context.Context, session *models.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Sessions[session.ID] = &models.User{ID: session.UserID}
	return nil
}

func (r *AuthRepo) GetSessionUser(_ context.Context, sessionID string) (*models.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.Sessions[sessionID]; ok {
		return u, nil
	}
	return nil, errors.New("session not found")
}

func (r *AuthRepo) DeleteSession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.Sessions, sessionID)
	return nil
}

// ---------------------------------------------------------------------------
// OrderRepository
// ---------------------------------------------------------------------------

// OrderRepo is a fake domain.OrderRepository that records every Execute call and keeps the orders it
// created in memory.
//
// Defaults: EnsureStock assigns stock IDs per distinct orderbook id, starting at 1. FindByIdempotencyKey
// searches the orders Execute created (ErrOrderNotFound otherwise). Execute returns the order already
// stored under the user's idempotency key with created=false, or else stores and returns a FILLED order
// with created=true. It never checks cash or shares: set ExecuteFn to simulate those errors.
type OrderRepo struct {
	mu       sync.Mutex
	Executed []domain.ExecuteOrderParams
	Orders   []*domain.Order
	stocks   []models.Stock

	EnsureStockFn          func(ctx context.Context, orderbookID, symbol, name string) (uint, error)
	FindByIdempotencyKeyFn func(ctx context.Context, userID uint, key string) (*domain.Order, error)
	ExecuteFn              func(ctx context.Context, p domain.ExecuteOrderParams) (*domain.Order, bool, error)

	// keys maps each created order (by index in Orders) to its user and idempotency key.
	keys []orderKey
}

type orderKey struct {
	userID uint
	key    string
}

func (r *OrderRepo) EnsureStock(ctx context.Context, orderbookID, symbol, name string) (uint, error) {
	if r.EnsureStockFn != nil {
		return r.EnsureStockFn(ctx, orderbookID, symbol, name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.stocks {
		if s.OrderbookID == orderbookID {
			return s.ID, nil
		}
	}
	id := uint(len(r.stocks) + 1)
	r.stocks = append(r.stocks, models.Stock{ID: id, OrderbookID: orderbookID, Symbol: symbol, Name: name})
	return id, nil
}

func (r *OrderRepo) FindByIdempotencyKey(ctx context.Context, userID uint, key string) (*domain.Order, error) {
	if r.FindByIdempotencyKeyFn != nil {
		return r.FindByIdempotencyKeyFn(ctx, userID, key)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if o := r.findLocked(userID, key); o != nil {
		return o, nil
	}
	return nil, domain.ErrOrderNotFound
}

func (r *OrderRepo) Execute(ctx context.Context, p domain.ExecuteOrderParams) (*domain.Order, bool, error) {
	r.mu.Lock()
	r.Executed = append(r.Executed, p)
	r.mu.Unlock()
	if r.ExecuteFn != nil {
		return r.ExecuteFn(ctx, p)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if p.IdempotencyKey != "" {
		if o := r.findLocked(p.UserID, p.IdempotencyKey); o != nil {
			return o, false, nil
		}
	}
	var stock models.Stock
	for _, s := range r.stocks {
		if s.ID == p.StockID {
			stock = s
		}
	}
	now := time.Date(2026, 10, 25, 9, 41, 0, 0, time.UTC).Add(time.Duration(len(r.Orders)) * time.Second)
	o := &domain.Order{
		ID:          uint(len(r.Orders) + 1),
		OrderbookID: stock.OrderbookID,
		Symbol:      stock.Symbol,
		Name:        stock.Name,
		Side:        p.Side,
		Type:        p.Type,
		Status:      domain.OrderStatusFilled,
		Quantity:    p.Quantity,
		FillPrice:   p.FillPrice,
		Commission:  p.Commission,
		Total:       p.Total,
		CreatedAt:   now,
		FilledAt:    &now,
	}
	r.Orders = append(r.Orders, o)
	r.keys = append(r.keys, orderKey{userID: p.UserID, key: p.IdempotencyKey})
	cp := *o
	return &cp, true, nil
}

// findLocked returns a copy of the user's order stored under key, or nil. r.mu must be held.
func (r *OrderRepo) findLocked(userID uint, key string) *domain.Order {
	for i, k := range r.keys {
		if k.userID == userID && k.key == key && key != "" {
			cp := *r.Orders[i]
			return &cp
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// WatchlistRepository
// ---------------------------------------------------------------------------

// WatchlistRepo is a fake domain.WatchlistRepository.
type WatchlistRepo struct {
	CreateFn            func(ctx context.Context, userID uint, name string) (*models.Watchlist, error)
	GetUserWatchlistsFn func(ctx context.Context, userID uint) ([]models.Watchlist, error)
	DeleteFn            func(ctx context.Context, userID, watchlistID uint) error
	AddItemFn           func(ctx context.Context, userID, watchlistID, stockID uint) error
	RemoveItemFn        func(ctx context.Context, userID, watchlistID uint, symbol string) error
	EnsureStockFn       func(ctx context.Context, symbol, name string) (*models.Stock, error)
}

func (r *WatchlistRepo) Create(ctx context.Context, userID uint, name string) (*models.Watchlist, error) {
	if r.CreateFn != nil {
		return r.CreateFn(ctx, userID, name)
	}
	return &models.Watchlist{ID: 1, UserID: userID, Name: name}, nil
}

func (r *WatchlistRepo) GetUserWatchlists(ctx context.Context, userID uint) ([]models.Watchlist, error) {
	if r.GetUserWatchlistsFn != nil {
		return r.GetUserWatchlistsFn(ctx, userID)
	}
	return []models.Watchlist{}, nil
}

func (r *WatchlistRepo) Delete(ctx context.Context, userID, watchlistID uint) error {
	if r.DeleteFn != nil {
		return r.DeleteFn(ctx, userID, watchlistID)
	}
	return nil
}

func (r *WatchlistRepo) AddItem(ctx context.Context, userID, watchlistID, stockID uint) error {
	if r.AddItemFn != nil {
		return r.AddItemFn(ctx, userID, watchlistID, stockID)
	}
	return nil
}

func (r *WatchlistRepo) RemoveItem(ctx context.Context, userID, watchlistID uint, symbol string) error {
	if r.RemoveItemFn != nil {
		return r.RemoveItemFn(ctx, userID, watchlistID, symbol)
	}
	return nil
}

func (r *WatchlistRepo) EnsureStock(ctx context.Context, symbol, name string) (*models.Stock, error) {
	if r.EnsureStockFn != nil {
		return r.EnsureStockFn(ctx, symbol, name)
	}
	return &models.Stock{ID: 1, Symbol: symbol, Name: name}, nil
}

// ---------------------------------------------------------------------------
// PortfolioRepository
// ---------------------------------------------------------------------------

// PortfolioRepo is a fake domain.PortfolioRepository returning fixed positions.
type PortfolioRepo struct {
	Positions []models.Position
	Err       error
}

func (r *PortfolioRepo) GetUserPositions(_ context.Context, _ uint) ([]models.Position, error) {
	return r.Positions, r.Err
}
