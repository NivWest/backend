package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// Order execution (story NW-04). Shapes: api/openapi.yaml#/components/schemas/OrderRequest and Order.
// Money is computed with decimal.Decimal and serialized as JSON numbers rounded half-up to 2 decimals.

// OrderSide is BUY (fills at the ask) or SELL (fills at the bid).
type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

// OrderType is the order kind. Only MARKET exists until NW-08 adds limit and stop orders.
type OrderType string

const OrderTypeMarket OrderType = "MARKET"

// OrderStatus is the lifecycle state stored in models.Order.Status. Market orders are written FILLED;
// OPEN, CANCELLED and REJECTED are reserved for pending orders (NW-08).
type OrderStatus string

const (
	OrderStatusOpen      OrderStatus = "OPEN"
	OrderStatusFilled    OrderStatus = "FILLED"
	OrderStatusCancelled OrderStatus = "CANCELLED"
	OrderStatusRejected  OrderStatus = "REJECTED"
)

// IdempotencyKeyHeader is the optional request header that makes POST /orders safe to retry.
// Keys are 1–255 printable ASCII characters and unique per user.
const IdempotencyKeyHeader = "Idempotency-Key"

// MaxIdempotencyKeyLength is the longest accepted Idempotency-Key (the column is varchar(255)).
const MaxIdempotencyKeyLength = 255

// Order error codes, returned as ErrorResponse.Code.
const (
	CodeInsufficientFunds  = "INSUFFICIENT_FUNDS"
	CodeInsufficientShares = "INSUFFICIENT_SHARES"
	CodeConflict           = "CONFLICT"
)

var (
	// ErrInsufficientFunds means a BUY's total exceeds the user's cash. Handlers map it to 422 INSUFFICIENT_FUNDS.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrInsufficientShares means a SELL's quantity exceeds the position. Handlers map it to 422 INSUFFICIENT_SHARES.
	ErrInsufficientShares = errors.New("insufficient shares")
	// ErrIdempotencyKeyReused means the Idempotency-Key was already used by this user for a different
	// order (another orderbook_id, side, quantity or type). Handlers map it to 409 CONFLICT.
	ErrIdempotencyKeyReused = errors.New("idempotency key already used for a different order")
	// ErrOrderNotFound means no order matches (e.g. no order with this idempotency key yet).
	ErrOrderNotFound = errors.New("order not found")
)

// OrderRequest is the body of POST /orders (#/components/schemas/OrderRequest). The service validates it
// (non-blank orderbook_id, side BUY or SELL, quantity > 0 with at most 4 decimals, type empty or MARKET)
// and answers a *ValidationError otherwise. Quantity accepts a JSON number.
type OrderRequest struct {
	OrderbookID string          `json:"orderbook_id"`
	Side        OrderSide       `json:"side"`
	Quantity    decimal.Decimal `json:"quantity"`
	Type        OrderType       `json:"type,omitempty"`
}

// Order is an order and its fill (#/components/schemas/Order). Symbol and Name come from the stock row.
// It marshals money fields as JSON numbers with 2 decimals and Quantity with up to 4 decimals.
type Order struct {
	ID          uint
	OrderbookID string
	Symbol      string
	Name        string
	Side        OrderSide
	Type        OrderType
	Status      OrderStatus
	Quantity    decimal.Decimal
	FillPrice   decimal.Decimal
	Commission  decimal.Decimal
	// Total is the cash moved: gross + commission for a BUY (debited), gross − commission for a SELL (credited).
	Total     decimal.Decimal
	CreatedAt time.Time
	FilledAt  *time.Time
}

type orderJSON struct {
	ID          uint        `json:"id"`
	OrderbookID string      `json:"orderbook_id"`
	Symbol      string      `json:"symbol"`
	Name        string      `json:"name"`
	Side        OrderSide   `json:"side"`
	Type        OrderType   `json:"type"`
	Status      OrderStatus `json:"status"`
	Quantity    json.Number `json:"quantity"`
	FillPrice   json.Number `json:"fill_price"`
	Commission  json.Number `json:"commission"`
	Total       json.Number `json:"total"`
	CreatedAt   time.Time   `json:"created_at"`
	FilledAt    *time.Time  `json:"filled_at"`
}

// MarshalJSON writes the wire shape of #/components/schemas/Order; timestamps are UTC.
func (o Order) MarshalJSON() ([]byte, error) {
	var filledAt *time.Time
	if o.FilledAt != nil {
		t := o.FilledAt.UTC()
		filledAt = &t
	}
	return json.Marshal(orderJSON{
		ID:          o.ID,
		OrderbookID: o.OrderbookID,
		Symbol:      o.Symbol,
		Name:        o.Name,
		Side:        o.Side,
		Type:        o.Type,
		Status:      o.Status,
		Quantity:    json.Number(o.Quantity.Round(4).String()),
		FillPrice:   json.Number(o.FillPrice.StringFixed(2)),
		Commission:  json.Number(o.Commission.StringFixed(2)),
		Total:       json.Number(o.Total.StringFixed(2)),
		CreatedAt:   o.CreatedAt.UTC(),
		FilledAt:    filledAt,
	})
}

// MoneyFloat rounds d half-up to 2 decimals for float64 JSON fields (e.g. ProfileResponse.Balance).
func MoneyFloat(d decimal.Decimal) float64 {
	return d.Round(2).InexactFloat64()
}

// ExecuteOrderParams is a priced market order for OrderRepository.Execute. Prices are already rounded
// (FillPrice, Commission and Total to 2 decimals) by the service's pricing function.
type ExecuteOrderParams struct {
	UserID     uint
	StockID    uint
	Side       OrderSide
	Type       OrderType
	Quantity   decimal.Decimal
	FillPrice  decimal.Decimal
	Commission decimal.Decimal
	// Total is debited from (BUY) or credited to (SELL) the user's balance.
	Total decimal.Decimal
	// IdempotencyKey is "" when the request had no Idempotency-Key header (stored as NULL).
	IdempotencyKey string
}

// OrderRepository persists orders, cash and positions. Implementation: internal/repository/order.go.
type OrderRepository interface {
	// EnsureStock returns the id of the stock row for orderbookID, creating it with symbol and name
	// when missing. Safe under concurrency: concurrent first orders for one instrument get the same row.
	EnsureStock(ctx context.Context, orderbookID, symbol, name string) (stockID uint, err error)

	// FindByIdempotencyKey returns the user's order created with key, or ErrOrderNotFound.
	FindByIdempotencyKey(ctx context.Context, userID uint, key string) (*Order, error)

	// Execute runs one DB transaction: lock the user row (SELECT … FOR UPDATE), lock the position, then
	// check the idempotency key. When the key already has an order it returns that order with
	// created=false and changes nothing. Otherwise it verifies cash (BUY: balance >= Total, else
	// ErrInsufficientFunds) or shares (SELL: position >= Quantity, else ErrInsufficientShares), moves
	// the cash, updates the position (BUY: weighted average fill price; SELL: average unchanged, the row
	// is deleted at zero), inserts a FILLED order with filled_at = now and returns it with created=true.
	// A unique-index violation on (user_id, idempotency_key) also yields the existing order, created=false.
	Execute(ctx context.Context, p ExecuteOrderParams) (order *Order, created bool, err error)
}
