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
	_ domain.StockProvider       = (*StockProvider)(nil)
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

// ExecutedOrder records the arguments of an OrderRepo.ExecuteTransaction call.
type ExecutedOrder struct {
	UserID, StockID        uint
	Type                   string
	Quantity, Price, Total float64
}

// OrderRepo is a fake domain.OrderRepository that records executed orders.
type OrderRepo struct {
	mu       sync.Mutex
	Executed []ExecutedOrder

	EnsureStockFn        func(ctx context.Context, symbol, name string) (*models.Stock, error)
	GetPositionFn        func(ctx context.Context, userID, stockID uint) (*models.Position, error)
	ExecuteTransactionFn func(ctx context.Context, o ExecutedOrder) error
}

func (r *OrderRepo) EnsureStock(ctx context.Context, symbol, name string) (*models.Stock, error) {
	if r.EnsureStockFn != nil {
		return r.EnsureStockFn(ctx, symbol, name)
	}
	return &models.Stock{ID: 1, Symbol: symbol, Name: name}, nil
}

func (r *OrderRepo) GetPosition(ctx context.Context, userID, stockID uint) (*models.Position, error) {
	if r.GetPositionFn != nil {
		return r.GetPositionFn(ctx, userID, stockID)
	}
	return nil, nil
}

func (r *OrderRepo) ExecuteTransaction(ctx context.Context, userID, stockID uint, orderType string, quantity, price, total float64) error {
	o := ExecutedOrder{UserID: userID, StockID: stockID, Type: orderType, Quantity: quantity, Price: price, Total: total}
	if r.ExecuteTransactionFn != nil {
		if err := r.ExecuteTransactionFn(ctx, o); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Executed = append(r.Executed, o)
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

// ---------------------------------------------------------------------------
// StockProvider
// ---------------------------------------------------------------------------

// StockProvider is a fake domain.StockProvider. Unset functions return ErrNotConfigured.
type StockProvider struct {
	SearchFn          func(ctx context.Context, query string) ([]byte, error)
	GetPriceChartFn   func(ctx context.Context, orderbookID, timePeriod string) ([]byte, error)
	GetStockDetailsFn func(ctx context.Context, orderbookID string) ([]byte, error)
	GetStockQuoteFn   func(ctx context.Context, orderbookID string) ([]byte, error)
}

// QuoteJSON is a convenience for a StockProvider quote returning a fixed JSON payload.
func QuoteJSON(payload string) func(context.Context, string) ([]byte, error) {
	return func(context.Context, string) ([]byte, error) { return []byte(payload), nil }
}

func (p *StockProvider) Search(ctx context.Context, query string) ([]byte, error) {
	if p.SearchFn != nil {
		return p.SearchFn(ctx, query)
	}
	return nil, ErrNotConfigured
}

func (p *StockProvider) GetPriceChart(ctx context.Context, orderbookID, timePeriod string) ([]byte, error) {
	if p.GetPriceChartFn != nil {
		return p.GetPriceChartFn(ctx, orderbookID, timePeriod)
	}
	return nil, ErrNotConfigured
}

func (p *StockProvider) GetStockDetails(ctx context.Context, orderbookID string) ([]byte, error) {
	if p.GetStockDetailsFn != nil {
		return p.GetStockDetailsFn(ctx, orderbookID)
	}
	return nil, ErrNotConfigured
}

func (p *StockProvider) GetStockQuote(ctx context.Context, orderbookID string) ([]byte, error) {
	if p.GetStockQuoteFn != nil {
		return p.GetStockQuoteFn(ctx, orderbookID)
	}
	return nil, ErrNotConfigured
}
