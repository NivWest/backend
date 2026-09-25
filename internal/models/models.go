package models

import (
	"gorm.io/gorm"
	"time"
)

type User struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	GoogleID  string         `gorm:"uniqueIndex;not null" json:"google_id"`
	Email     string         `gorm:"uniqueIndex;not null" json:"email"`
	Name      string         `json:"name"`
	Role      string         `gorm:"type:varchar(20);not null;default:member" json:"role"`
	Balance   float64        `gorm:"type:numeric(15,2);default:100000.00" json:"balance"` // Simulator starting cash
	Positions []Position     `json:"positions,omitempty"`
	Favorites []Stock        `gorm:"many2many:user_favorites;" json:"favorites,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
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

type Stock struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	Symbol    string         `gorm:"uniqueIndex;not null" json:"symbol"`
	Name      string         `json:"name"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type Position struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"user_id"`
	StockID   uint      `gorm:"index;not null;constraint:OnDelete:CASCADE;" json:"stock_id"`
	Stock     Stock     `json:"stock,omitempty"`
	Quantity  float64   `gorm:"type:numeric(15,4);not null" json:"quantity"` // Fractional shares support
	AvgPrice  float64   `gorm:"type:numeric(15,2);not null" json:"avg_price"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

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
