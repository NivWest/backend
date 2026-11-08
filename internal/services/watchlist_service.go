package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

// maxConcurrentQuotes caps the quote requests one watchlist read runs in parallel.
const maxConcurrentQuotes = 8

// WatchlistService holds the watchlist rules (story NW-06): names of 1–50 characters, at most
// domain.MaxWatchlists lists per user, items keyed by orderbook_id and priced from the live quote.
type WatchlistService struct {
	repo     domain.WatchlistRepository
	provider domain.MarketDataProvider
}

func NewWatchlistService(repo domain.WatchlistRepository, provider domain.MarketDataProvider) *WatchlistService {
	return &WatchlistService{repo: repo, provider: provider}
}

// List returns the user's lists with every item priced.
func (s *WatchlistService) List(ctx context.Context, userID uint) (*domain.WatchlistListResponse, error) {
	lists, err := s.repo.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	if lists == nil {
		lists = []domain.Watchlist{}
	}
	s.price(ctx, lists)
	return &domain.WatchlistListResponse{Items: lists, NextCursor: nil}, nil
}

// Get returns one of the user's lists with its items priced, or domain.ErrWatchlistNotFound.
func (s *WatchlistService) Get(ctx context.Context, userID, watchlistID uint) (*domain.Watchlist, error) {
	wl, err := s.repo.Get(ctx, userID, watchlistID)
	if err != nil {
		return nil, err
	}
	lists := []domain.Watchlist{*wl}
	s.price(ctx, lists)
	return &lists[0], nil
}

// Create validates the name and creates an empty list. Errors: *domain.ValidationError, domain.ErrWatchlistLimit.
func (s *WatchlistService) Create(ctx context.Context, userID uint, req domain.WatchlistNameRequest) (*domain.Watchlist, error) {
	name, err := normalizeWatchlistName(req.Name)
	if err != nil {
		return nil, err
	}
	return s.repo.Create(ctx, userID, name, domain.MaxWatchlists)
}

// Rename validates the name, renames the list and returns it priced.
// Errors: *domain.ValidationError, domain.ErrWatchlistNotFound.
func (s *WatchlistService) Rename(ctx context.Context, userID, watchlistID uint, req domain.WatchlistNameRequest) (*domain.Watchlist, error) {
	name, err := normalizeWatchlistName(req.Name)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Rename(ctx, userID, watchlistID, name); err != nil {
		return nil, err
	}
	return s.Get(ctx, userID, watchlistID)
}

// Delete removes one of the user's lists and its items. Errors: domain.ErrWatchlistNotFound.
func (s *WatchlistService) Delete(ctx context.Context, userID, watchlistID uint) error {
	return s.repo.Delete(ctx, userID, watchlistID)
}

// AddItem puts an instrument on one of the user's lists and returns the item with its current price.
// Ownership is checked before the instrument, so another user's list id never reveals anything else.
// Errors: *domain.ValidationError, domain.ErrWatchlistNotFound, domain.ErrInstrumentNotFound,
// domain.ErrMarketDataUnavailable (wrapped), domain.ErrDuplicateItem.
func (s *WatchlistService) AddItem(ctx context.Context, userID, watchlistID uint, req domain.WatchlistItemRequest) (*domain.WatchlistItem, error) {
	orderbookID := strings.TrimSpace(req.OrderbookID)
	if orderbookID == "" {
		return nil, &domain.ValidationError{Msg: "orderbook_id is required"}
	}
	if _, err := s.repo.Get(ctx, userID, watchlistID); err != nil {
		return nil, err
	}
	details, err := s.provider.Details(ctx, orderbookID)
	if err != nil {
		return nil, marketDataError(err)
	}
	stockID, err := s.repo.EnsureStock(ctx, orderbookID, details.Symbol, details.Name)
	if err != nil {
		return nil, fmt.Errorf("ensure stock %s: %w", orderbookID, err)
	}
	item, err := s.repo.AddItem(ctx, userID, watchlistID, stockID)
	if err != nil {
		return nil, err
	}
	if details.LastPrice > 0 {
		last := decimal.NewFromFloat(details.LastPrice)
		change := details.ChangePercent
		item.LastPrice, item.ChangePercent = &last, &change
	}
	return item, nil
}

// RemoveItem takes an instrument off one of the user's lists.
// Errors: *domain.ValidationError, domain.ErrWatchlistNotFound, domain.ErrWatchlistItemNotFound.
func (s *WatchlistService) RemoveItem(ctx context.Context, userID, watchlistID uint, orderbookID string) error {
	orderbookID = strings.TrimSpace(orderbookID)
	if orderbookID == "" {
		return &domain.ValidationError{Msg: "orderbook_id is required"}
	}
	return s.repo.RemoveItem(ctx, userID, watchlistID, orderbookID)
}

// price fills LastPrice and ChangePercent of every item from the live quote. Each distinct orderbook id is
// quoted once, at most maxConcurrentQuotes at a time. A failed quote (or one without a price) leaves the
// item's price nil instead of failing the whole read.
func (s *WatchlistService) price(ctx context.Context, lists []domain.Watchlist) {
	seen := map[string]bool{}
	var ids []string
	for _, wl := range lists {
		for _, item := range wl.Items {
			if !seen[item.OrderbookID] {
				seen[item.OrderbookID] = true
				ids = append(ids, item.OrderbookID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}

	quotes := make(map[string]domain.Quote, len(ids))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxConcurrentQuotes)
	)
	for _, id := range ids {
		wg.Add(1)
		go func(orderbookID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			quote, err := s.provider.Quote(ctx, orderbookID)
			if err != nil || quote.Last <= 0 {
				return
			}
			mu.Lock()
			quotes[orderbookID] = quote
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	for i := range lists {
		for j := range lists[i].Items {
			item := &lists[i].Items[j]
			quote, ok := quotes[item.OrderbookID]
			if !ok {
				continue
			}
			last := decimal.NewFromFloat(quote.Last)
			change := quote.ChangePercent
			item.LastPrice, item.ChangePercent = &last, &change
		}
	}
}

// normalizeWatchlistName trims name and requires 1–domain.MaxWatchlistNameLength characters.
func normalizeWatchlistName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &domain.ValidationError{Msg: "name is required"}
	}
	if utf8.RuneCountInString(name) > domain.MaxWatchlistNameLength {
		return "", &domain.ValidationError{Msg: fmt.Sprintf("name must be at most %d characters", domain.MaxWatchlistNameLength)}
	}
	return name, nil
}
