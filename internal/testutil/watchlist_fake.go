package testutil

import (
	"context"
	"sort"
	"sync"
	"time"

	"backend/internal/domain"
)

// WatchlistRepo is an in-memory domain.WatchlistRepository that enforces the same rules as the GORM one:
// every call is scoped by user id (someone else's list is ErrWatchlistNotFound), Create counts the user's
// lists against max (ErrWatchlistLimit), an orderbook id is on a list at most once (ErrDuplicateItem) and
// removing an absent one is ErrWatchlistItemNotFound. List ids start at 1; timestamps advance one second
// per write from a fixed clock.
//
// Each method has an optional Fn override (e.g. to inject a database error).
type WatchlistRepo struct {
	mu     sync.Mutex
	lists  map[uint]*fakeWatchlist
	nextID uint
	stocks []fakeStock
	tick   int

	ListFn        func(ctx context.Context, userID uint) ([]domain.Watchlist, error)
	GetFn         func(ctx context.Context, userID, watchlistID uint) (*domain.Watchlist, error)
	CreateFn      func(ctx context.Context, userID uint, name string, max int) (*domain.Watchlist, error)
	RenameFn      func(ctx context.Context, userID, watchlistID uint, name string) error
	DeleteFn      func(ctx context.Context, userID, watchlistID uint) error
	AddItemFn     func(ctx context.Context, userID, watchlistID, stockID uint) (*domain.WatchlistItem, error)
	RemoveItemFn  func(ctx context.Context, userID, watchlistID uint, orderbookID string) error
	EnsureStockFn func(ctx context.Context, orderbookID, symbol, name string) (uint, error)
}

var _ domain.WatchlistRepository = (*WatchlistRepo)(nil)

type fakeWatchlist struct {
	userID uint
	list   domain.Watchlist
}

type fakeStock struct {
	id                        uint
	orderbookID, symbol, name string
}

// NewWatchlistRepo returns an empty WatchlistRepo.
func NewWatchlistRepo() *WatchlistRepo {
	return &WatchlistRepo{lists: map[uint]*fakeWatchlist{}}
}

// now returns the fake clock and advances it. r.mu must be held.
func (r *WatchlistRepo) now() time.Time {
	r.tick++
	return time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC).Add(time.Duration(r.tick) * time.Second)
}

func (r *WatchlistRepo) init() {
	if r.lists == nil {
		r.lists = map[uint]*fakeWatchlist{}
	}
}

// owned returns the user's list or nil. r.mu must be held.
func (r *WatchlistRepo) owned(userID, watchlistID uint) *fakeWatchlist {
	r.init()
	if wl, ok := r.lists[watchlistID]; ok && wl.userID == userID {
		return wl
	}
	return nil
}

func copyWatchlist(wl domain.Watchlist) domain.Watchlist {
	wl.Items = append([]domain.WatchlistItem{}, wl.Items...)
	return wl
}

func (r *WatchlistRepo) List(ctx context.Context, userID uint) ([]domain.Watchlist, error) {
	if r.ListFn != nil {
		return r.ListFn(ctx, userID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	out := []domain.Watchlist{}
	for _, wl := range r.lists {
		if wl.userID == userID {
			out = append(out, copyWatchlist(wl.list))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *WatchlistRepo) Get(ctx context.Context, userID, watchlistID uint) (*domain.Watchlist, error) {
	if r.GetFn != nil {
		return r.GetFn(ctx, userID, watchlistID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wl := r.owned(userID, watchlistID)
	if wl == nil {
		return nil, domain.ErrWatchlistNotFound
	}
	cp := copyWatchlist(wl.list)
	return &cp, nil
}

func (r *WatchlistRepo) Create(ctx context.Context, userID uint, name string, max int) (*domain.Watchlist, error) {
	if r.CreateFn != nil {
		return r.CreateFn(ctx, userID, name, max)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	count := 0
	for _, wl := range r.lists {
		if wl.userID == userID {
			count++
		}
	}
	if count >= max {
		return nil, domain.ErrWatchlistLimit
	}
	r.nextID++
	wl := &fakeWatchlist{userID: userID, list: domain.Watchlist{ID: r.nextID, Name: name, Items: []domain.WatchlistItem{}, CreatedAt: r.now()}}
	r.lists[wl.list.ID] = wl
	cp := copyWatchlist(wl.list)
	return &cp, nil
}

func (r *WatchlistRepo) Rename(ctx context.Context, userID, watchlistID uint, name string) error {
	if r.RenameFn != nil {
		return r.RenameFn(ctx, userID, watchlistID, name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wl := r.owned(userID, watchlistID)
	if wl == nil {
		return domain.ErrWatchlistNotFound
	}
	wl.list.Name = name
	return nil
}

func (r *WatchlistRepo) Delete(ctx context.Context, userID, watchlistID uint) error {
	if r.DeleteFn != nil {
		return r.DeleteFn(ctx, userID, watchlistID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owned(userID, watchlistID) == nil {
		return domain.ErrWatchlistNotFound
	}
	delete(r.lists, watchlistID)
	return nil
}

func (r *WatchlistRepo) AddItem(ctx context.Context, userID, watchlistID, stockID uint) (*domain.WatchlistItem, error) {
	if r.AddItemFn != nil {
		return r.AddItemFn(ctx, userID, watchlistID, stockID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wl := r.owned(userID, watchlistID)
	if wl == nil {
		return nil, domain.ErrWatchlistNotFound
	}
	var stock *fakeStock
	for i := range r.stocks {
		if r.stocks[i].id == stockID {
			stock = &r.stocks[i]
		}
	}
	if stock == nil {
		return nil, ErrNotConfigured
	}
	for _, item := range wl.list.Items {
		if item.OrderbookID == stock.orderbookID {
			return nil, domain.ErrDuplicateItem
		}
	}
	item := domain.WatchlistItem{OrderbookID: stock.orderbookID, Symbol: stock.symbol, Name: stock.name, AddedAt: r.now()}
	wl.list.Items = append(wl.list.Items, item)
	return &item, nil
}

func (r *WatchlistRepo) RemoveItem(ctx context.Context, userID, watchlistID uint, orderbookID string) error {
	if r.RemoveItemFn != nil {
		return r.RemoveItemFn(ctx, userID, watchlistID, orderbookID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wl := r.owned(userID, watchlistID)
	if wl == nil {
		return domain.ErrWatchlistNotFound
	}
	for i, item := range wl.list.Items {
		if item.OrderbookID == orderbookID {
			wl.list.Items = append(wl.list.Items[:i], wl.list.Items[i+1:]...)
			return nil
		}
	}
	return domain.ErrWatchlistItemNotFound
}

func (r *WatchlistRepo) EnsureStock(ctx context.Context, orderbookID, symbol, name string) (uint, error) {
	if r.EnsureStockFn != nil {
		return r.EnsureStockFn(ctx, orderbookID, symbol, name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.stocks {
		if s.orderbookID == orderbookID {
			return s.id, nil
		}
	}
	id := uint(len(r.stocks) + 1)
	r.stocks = append(r.stocks, fakeStock{id: id, orderbookID: orderbookID, symbol: symbol, name: name})
	return id, nil
}
