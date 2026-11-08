package models

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// Money columns are numeric(18,2) and quantities numeric(18,4), held as decimal.Decimal (story NW-04).

type User struct {
	ID       uint            `gorm:"primaryKey" json:"id"`
	GoogleID string          `gorm:"uniqueIndex;not null" json:"google_id"`
	Email    string          `gorm:"uniqueIndex;not null" json:"email"`
	Name     string          `json:"name"`
	Role     string          `gorm:"type:varchar(20);not null;default:member" json:"role"`
	Balance  decimal.Decimal `gorm:"type:numeric(18,2);not null;default:100000.00" json:"balance"` // Simulator starting cash
	// RealizedPnL accumulates sell total − avg_cost × quantity sold over every SELL (story NW-05).
	RealizedPnL decimal.Decimal `gorm:"column:realized_pnl;type:numeric(18,2);not null;default:0" json:"realized_pnl"`
	Positions   []Position      `json:"positions,omitempty"`
	Favorites   []Stock         `gorm:"many2many:user_favorites;" json:"favorites,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	DeletedAt   gorm.DeletedAt  `gorm:"index" json:"-"`
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
	ID       uint            `gorm:"primaryKey" json:"id"`
	UserID   uint            `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"user_id"`
	StockID  uint            `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock    Stock           `json:"stock,omitempty"`
	Quantity decimal.Decimal `gorm:"type:numeric(18,4);not null" json:"quantity"`  // Fractional shares support
	AvgPrice decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"avg_price"` // Average fill price, commission excluded
	// AvgCost is the cost basis per share, buy commission included: (Σ BUY totals of the open lot) ÷ quantity.
	// Selling leaves it unchanged (story NW-05). Kept at 8 decimals so avg_cost × quantity stays exact enough.
	AvgCost   decimal.Decimal `gorm:"type:numeric(20,8);not null;default:0" json:"avg_cost"`
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
	ID             uint            `gorm:"primaryKey;index:idx_orders_user_created_id,priority:3" json:"id"`
	UserID         uint            `gorm:"not null;index;uniqueIndex:idx_orders_user_idempotency_key,priority:1;index:idx_orders_user_created_id,priority:1;constraint:OnDelete:CASCADE;" json:"user_id"`
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
	CreatedAt      time.Time       `gorm:"index:idx_orders_user_created_id,priority:2" json:"created_at"` // Order history keyset (NW-05)
	UpdatedAt      time.Time       `json:"updated_at"`
	FilledAt       *time.Time      `json:"filled_at"`
}

// Watchlist is a named list of stocks owned by one user (at most domain.MaxWatchlists per user). Names are
// validated to 1–50 characters by the service; the column is wider for older rows.
type Watchlist struct {
	ID        uint            `gorm:"primaryKey" json:"id"`
	UserID    uint            `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"user_id"`
	Name      string          `gorm:"type:varchar(100);not null" json:"name"`
	Items     []WatchlistItem `json:"items,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// WatchlistItem is one stock on one list. (watchlist_id, stock_id) is unique (story NW-06), so an instrument is
// on a list at most once, also under concurrent adds.
type WatchlistItem struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	WatchlistID uint      `gorm:"index;not null;uniqueIndex:idx_watchlist_items_watchlist_stock,priority:1;constraint:OnDelete:CASCADE;" json:"watchlist_id"`
	StockID     uint      `gorm:"index;not null;uniqueIndex:idx_watchlist_items_watchlist_stock,priority:2;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock       Stock     `json:"stock,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// AIUsage is the LLM usage ledger (story NW-14): one row per model call, written by services.AIGateway
// after every call, successful or not. The per-user daily budget sums today's rows.
type AIUsage struct {
	ID               uint            `gorm:"primaryKey"`
	UserID           uint            `gorm:"not null;index:idx_ai_usages_user_created,priority:1"`
	Feature          string          `gorm:"type:varchar(40);not null"`
	Model            string          `gorm:"type:varchar(80);not null"`
	PromptVersion    string          `gorm:"type:varchar(60);not null"`
	InputTokens      int64           `gorm:"not null;default:0"`
	OutputTokens     int64           `gorm:"not null;default:0"`
	CacheReadTokens  int64           `gorm:"not null;default:0"`
	CacheWriteTokens int64           `gorm:"not null;default:0"`
	LatencyMs        int64           `gorm:"not null;default:0"`
	Status           string          `gorm:"type:varchar(10);not null"` // ok | error | refused
	FallbackModel    string          `gorm:"type:varchar(80);not null;default:''"`
	CostUSD          decimal.Decimal `gorm:"column:cost_usd;type:numeric(14,6);not null;default:0"`
	CreatedAt        time.Time       `gorm:"not null;index:idx_ai_usages_user_created,priority:2"`
}

// AIInstrumentBrief caches one AI brief per instrument, UTC day and prompt version, shared by every user (story NW-14).
type AIInstrumentBrief struct {
	ID            uint      `gorm:"primaryKey"`
	OrderbookID   string    `gorm:"not null;uniqueIndex:idx_ai_briefs_key,priority:1"`
	UTCDate       string    `gorm:"column:utc_date;type:varchar(10);not null;uniqueIndex:idx_ai_briefs_key,priority:2"`
	PromptVersion string    `gorm:"type:varchar(60);not null;uniqueIndex:idx_ai_briefs_key,priority:3"`
	Model         string    `gorm:"type:varchar(80);not null"`
	BriefJSON     string    `gorm:"column:brief_json;type:text;not null"`
	GeneratedAt   time.Time `gorm:"not null"`
}

// EquitySnapshot is one valuation of a user's account at time T (story NW-10): cash, holdings at the last
// price, the vault (0 until NW-11) and their sum. One is written after every fill and every SNAPSHOT_INTERVAL
// for users with an order. (user_id, t) is indexed for the per-user history and analytics reads.
type EquitySnapshot struct {
	ID            uint            `gorm:"primaryKey" json:"id"`
	UserID        uint            `gorm:"not null;index:idx_equity_snapshots_user_t,priority:1" json:"user_id"`
	T             time.Time       `gorm:"column:t;not null;index:idx_equity_snapshots_user_t,priority:2" json:"t"`
	Cash          decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"cash"`
	HoldingsValue decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"holdings_value"`
	VaultValue    decimal.Decimal `gorm:"type:numeric(18,2);not null;default:0" json:"vault_value"`
	Equity        decimal.Decimal `gorm:"type:numeric(18,2);not null" json:"equity"`
}
