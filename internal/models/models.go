package models

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Money columns are numeric(18,2) and quantities numeric(18,4), held as decimal.Decimal (story NW-04).

type User struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	GoogleID  string          `gorm:"uniqueIndex;not null" json:"google_id"`
	Email     string          `gorm:"uniqueIndex;not null" json:"email"`
	Name      string          `json:"name"`
	Role      string          `gorm:"type:varchar(20);not null;default:member" json:"role"`
	Balance   decimal.Decimal `gorm:"type:numeric(18,2);not null;default:100000.00" json:"balance"` // Simulator starting cash
	Positions []Position      `json:"positions,omitempty"`
	Favorites []Stock         `gorm:"many2many:user_favorites;" json:"favorites,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	DeletedAt gorm.DeletedAt  `gorm:"index" json:"-"`
}

type Session struct {
	ID        string    `gorm:"primaryKey;size:64" json:"-"`
	UserID    uint      `gorm:"index;not null" json:"-"`
	User      User      `json:"-"`
	ExpiresAt time.Time `gorm:"index;not null" json:"-"`
	CreatedAt time.Time `json:"-"`
}

type OAuthState struct {
	State        string    `gorm:"primaryKey;size:128" json:"-"`
	PKCEVerifier string    `gorm:"not null;size:128" json:"-"`
	ExpiresAt    time.Time `gorm:"index;not null" json:"-"`
}

// Stock is an instrument someone has traded or watched, identified by its market data orderbook id.
// Symbols are not unique across markets, so they are only indexed for lookup.
type Stock struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	OrderbookID string         `gorm:"uniqueIndex;not null" json:"orderbook_id"`
	Symbol      string         `gorm:"index:idx_stocks_symbol_lookup;not null" json:"symbol"`
	Name        string         `json:"name"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type Position struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	UserID    uint            `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"user_id"`
	StockID   uint            `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock     Stock           `json:"stock,omitempty"`
	Quantity  decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"quantity"` // Fractional shares support
	AvgPrice  decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"avg_price"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Transaction is the pre-NW-04 trade log. It is no longer written (orders live in Order) and is
// kept only so existing rows survive AutoMigrate and guest purges still remove them.
type Transaction struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"user_id"`
	StockID   uint      `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock     Stock     `json:"stock,omitempty"`
	Type      string    `gorm:"type:varchar(10);not null" json:"type"` // "BUY" or "SELL"
	Quantity  float64   `gorm:"type:numeric(15,4);not null" json:"quantity"`
	Price     float64   `gorm:"type:numeric(15,2);not null" json:"price"`
	Total     float64   `gorm:"type:numeric(15,2);not null" json:"total"`
	CreatedAt time.Time `json:"created_at"`
}

// Order is one order and, once executed, its fill (story NW-04). Status is one of the
// domain.OrderStatus values; market orders are written FILLED in the same DB transaction
// that moves the cash and the position. IdempotencyKey is unique per user (NULL when the
// request had no Idempotency-Key header), so concurrent retries collapse into one row.
type Order struct {
	ID             uint            `gorm:"primaryKey" json:"id"`
	UserID         uint            `gorm:"not null;index;uniqueIndex:idx_orders_user_idempotency_key,priority:1;constraint:OnDelete:CASCADE;" json:"user_id"`
	StockID        uint            `gorm:"not null;index;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock          Stock           `json:"stock,omitempty"`
	Side           string          `gorm:"type:varchar(4);not null" json:"side"`
	Type           string          `gorm:"type:varchar(10);not null;default:MARKET" json:"type"`
	Status         string          `gorm:"type:varchar(10);not null" json:"status"`
	Quantity       decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"quantity"`
	FillPrice      decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"fill_price"`
	Commission     decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"commission"`
	Total          decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"total"`
	IdempotencyKey *string         `gorm:"size:255;uniqueIndex:idx_orders_user_idempotency_key,priority:2" json:"-"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	FilledAt       *time.Time      `json:"filled_at"`
}

type Watchlist struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	UserID    uint            `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"user_id"`
	Name      string          `gorm:"type:varchar(100);not null" json:"name"`
	Items     []WatchlistItem `json:"items,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type WatchlistItem struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	WatchlistID uint      `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"watchlist_id"`
	StockID     uint      `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock       Stock     `json:"stock,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
