package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// Watchlists (story NW-06). Shapes: api/openapi.yaml#/components/schemas/Watchlist, WatchlistItem and
// WatchlistListResponse. Items are keyed by orderbook_id and every repository call is scoped by user id,
// so another user's list is indistinguishable from a missing one (ErrWatchlistNotFound → 404).

// MaxWatchlists is the most lists one user may have; creating another is ErrWatchlistLimit.
const MaxWatchlists = 10

// MaxWatchlistNameLength is the longest list name, in characters, after trimming.
const MaxWatchlistNameLength = 50

// Watchlist error codes, returned as ErrorResponse.Code. A duplicate item uses CodeConflict.
const (
	CodeWatchlistNotFound     = "WATCHLIST_NOT_FOUND"
	CodeWatchlistItemNotFound = "WATCHLIST_ITEM_NOT_FOUND"
	CodeWatchlistLimitReached = "WATCHLIST_LIMIT_REACHED"
)

var (
	// ErrWatchlistNotFound means no list with this id belongs to the user. Handlers map it to 404 WATCHLIST_NOT_FOUND.
	ErrWatchlistNotFound = errors.New("watchlist not found")
	// ErrWatchlistItemNotFound means the instrument is not on the list. Handlers map it to 404 WATCHLIST_ITEM_NOT_FOUND.
	ErrWatchlistItemNotFound = errors.New("instrument is not on this watchlist")
	// ErrDuplicateItem means the instrument is already on the list. Handlers map it to 409 CONFLICT.
	ErrDuplicateItem = errors.New("instrument is already on this watchlist")
	// ErrWatchlistLimit means the user already has MaxWatchlists lists. Handlers map it to 422 WATCHLIST_LIMIT_REACHED.
	ErrWatchlistLimit = errors.New("you can have at most 10 watchlists")
)

// WatchlistNameRequest is the body of POST /watchlists and PATCH /watchlists/{id}. The service trims Name and
// requires 1–MaxWatchlistNameLength characters.
type WatchlistNameRequest struct {
	Name string `json:"name"`
}

// WatchlistItemRequest is the body of POST /watchlists/{id}/items.
type WatchlistItemRequest struct {
	OrderbookID string `json:"orderbook_id"`
}

// WatchlistItem is one instrument on a list. The repository fills the stock fields and AddedAt; the service
// fills LastPrice and ChangePercent from the live quote and leaves them nil when the quote is unavailable.
type WatchlistItem struct {
	OrderbookID   string
	Symbol        string
	Name          string
	LastPrice     *decimal.Decimal
	ChangePercent *float64
	AddedAt       time.Time
}

type watchlistItemJSON struct {
	OrderbookID   string       `json:"orderbook_id"`
	Symbol        string       `json:"symbol"`
	Name          string       `json:"name"`
	LastPrice     *json.Number `json:"last_price"`
	ChangePercent *float64     `json:"change_percent"`
	AddedAt       time.Time    `json:"added_at"`
}

// MarshalJSON writes #/components/schemas/WatchlistItem: last_price as a JSON number with 2 decimals (or null),
// added_at in UTC.
func (i WatchlistItem) MarshalJSON() ([]byte, error) {
	out := watchlistItemJSON{
		OrderbookID:   i.OrderbookID,
		Symbol:        i.Symbol,
		Name:          i.Name,
		ChangePercent: i.ChangePercent,
		AddedAt:       i.AddedAt.UTC(),
	}
	if i.LastPrice != nil {
		n := json.Number(i.LastPrice.StringFixed(2))
		out.LastPrice = &n
	}
	return json.Marshal(out)
}

// Watchlist is a named list owned by one user, with its items oldest first.
type Watchlist struct {
	ID        uint
	Name      string
	Items     []WatchlistItem
	CreatedAt time.Time
}

type watchlistJSON struct {
	ID        uint            `json:"id"`
	Name      string          `json:"name"`
	Items     []WatchlistItem `json:"items"`
	CreatedAt time.Time       `json:"created_at"`
}

// MarshalJSON writes #/components/schemas/Watchlist; items is never null.
func (w Watchlist) MarshalJSON() ([]byte, error) {
	items := w.Items
	if items == nil {
		items = []WatchlistItem{}
	}
	return json.Marshal(watchlistJSON{ID: w.ID, Name: w.Name, Items: items, CreatedAt: w.CreatedAt.UTC()})
}

// WatchlistListResponse is the body of GET /watchlists. Items is never nil; NextCursor is always nil
// (a user has at most MaxWatchlists lists).
type WatchlistListResponse struct {
	Items      []Watchlist `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

// WatchlistRepository persists watchlists. Every method is scoped by userID: a list id that belongs to someone
// else yields ErrWatchlistNotFound, exactly like a missing one. Implementation: internal/repository/watchlist.go.
type WatchlistRepository interface {
	// List returns the user's lists, oldest first, each with its items oldest first.
	List(ctx context.Context, userID uint) ([]Watchlist, error)
	// Get returns one of the user's lists with its items, or ErrWatchlistNotFound.
	Get(ctx context.Context, userID, watchlistID uint) (*Watchlist, error)
	// Create inserts an empty list. In one transaction that locks the user row it counts the user's lists and
	// returns ErrWatchlistLimit when there are already max, so concurrent creates cannot exceed it.
	Create(ctx context.Context, userID uint, name string, max int) (*Watchlist, error)
	// Rename sets the name of one of the user's lists, or returns ErrWatchlistNotFound.
	Rename(ctx context.Context, userID, watchlistID uint, name string) error
	// Delete removes one of the user's lists and its items, or returns ErrWatchlistNotFound.
	Delete(ctx context.Context, userID, watchlistID uint) error
	// AddItem puts the stock on one of the user's lists and returns the stored item (prices unset).
	// Errors: ErrWatchlistNotFound, ErrDuplicateItem (unique (watchlist_id, stock_id), also under concurrency).
	AddItem(ctx context.Context, userID, watchlistID, stockID uint) (*WatchlistItem, error)
	// RemoveItem takes the instrument off one of the user's lists.
	// Errors: ErrWatchlistNotFound, ErrWatchlistItemNotFound.
	RemoveItem(ctx context.Context, userID, watchlistID uint, orderbookID string) error
	// EnsureStock returns the id of the stock row for orderbookID, creating it with symbol and name when
	// missing (race-safe, shared with orders).
	EnsureStock(ctx context.Context, orderbookID, symbol, name string) (stockID uint, err error)
}
