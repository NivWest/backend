package models

import (
	"time"
	"gorm.io/gorm"
)

type User struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	GoogleID  string         `gorm:"uniqueIndex;not null" json:"google_id"`
	Email     string         `gorm:"uniqueIndex;not null" json:"email"`
	Name      string         `json:"name"`
	Balance   float64        `gorm:"type:numeric(15,2);default:100000.00" json:"balance"` // Simulator starting cash
	Positions []Position     `json:"positions,omitempty"`
	Favorites []Stock        `gorm:"many2many:user_favorites;" json:"favorites,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
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
	ID        uint           `gorm:"primaryKey" json:"id"`
	UserID    uint           `gorm:"index;not null" json:"user_id"`
	StockID   uint           `gorm:"index;not null" json:"stock_id"`
	Stock     Stock          `json:"stock,omitempty"`
	Quantity  float64        `gorm:"type:numeric(15,4);not null" json:"quantity"` // Fractional shares support
	AvgPrice  float64        `gorm:"type:numeric(15,2);not null" json:"avg_price"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}
