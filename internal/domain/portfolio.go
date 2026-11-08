package domain

import (
	"time"
)

// Order history and portfolio (story NW-05). Shapes: api/openapi.yaml#/components/schemas/OrderPage,
// Portfolio and PortfolioPosition. Money is computed with decimal.Decimal and only converted with MoneyFloat
// (half-up, 2 decimals) when a DTO below is filled; percentages are rounded half-up to 2 decimals too.

// Order list paging limits for GET /orders?limit=.
const (
	DefaultOrderPageLimit = 20
	MaxOrderPageLimit     = 100
)

// OrderCursor is the keyset position of an order in the newest-first list: (created_at desc, id desc).
// On the wire it is opaque (URL-safe base64, see OrderService); the repository only sees this struct.
type OrderCursor struct {
	CreatedAt time.Time
	ID        uint
}

// OrderPage is the body of GET /orders (#/components/schemas/OrderPage). Items is never nil (an empty
// page serializes as []); NextCursor is nil on the last page (serialized as null).
type OrderPage struct {
	Items      []Order `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// PortfolioPosition is one open position (#/components/schemas/PortfolioPosition). Money fields are SEK
// rounded half-up to 2 decimals, Quantity up to 4 decimals, percentages 2 decimals.
type PortfolioPosition struct {
	OrderbookID          string  `json:"orderbook_id"`
	Symbol               string  `json:"symbol"`
	Name                 string  `json:"name"`
	Quantity             float64 `json:"quantity"`
	AvgCost              float64 `json:"avg_cost"`
	LastPrice            float64 `json:"last_price"`
	MarketValue          float64 `json:"market_value"`
	UnrealizedPnL        float64 `json:"unrealized_pnl"`
	UnrealizedPnLPercent float64 `json:"unrealized_pnl_percent"`
	DayChangePercent     float64 `json:"day_change_percent"`
	Weight               float64 `json:"weight"`
}

// Portfolio is the body of GET /portfolio (#/components/schemas/Portfolio). Positions are sorted by
// market value desc (then orderbook id asc); TopMovers holds at most MaxTopMovers positions sorted by
// day change desc (then orderbook id asc). Both slices are never nil.
type Portfolio struct {
	Cash          float64             `json:"cash"`
	HoldingsValue float64             `json:"holdings_value"`
	TotalEquity   float64             `json:"total_equity"`
	UnrealizedPnL float64             `json:"unrealized_pnl"`
	RealizedPnL   float64             `json:"realized_pnl"`
	Positions     []PortfolioPosition `json:"positions"`
	TopMovers     []PortfolioPosition `json:"top_movers"`
}

// MaxTopMovers caps Portfolio.TopMovers.
const MaxTopMovers = 5
