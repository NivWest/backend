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

// OrderType is the order kind. MARKET executes at once; LIMIT and STOP (story NW-08) are good-till-cancelled
// pending orders that the matching engine fills once the quote reaches their price.
type OrderType string

const (
	OrderTypeMarket OrderType = "MARKET"
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeStop   OrderType = "STOP"
)

// IsPending reports whether orders of this type rest OPEN until the matching engine fills them.
func (t OrderType) IsPending() bool { return t == OrderTypeLimit || t == OrderTypeStop }

// OrderStatus is the lifecycle state stored in models.Order.Status. Market orders are written FILLED.
// A LIMIT or STOP order starts OPEN and leaves it exactly once: to FILLED (matching engine), CANCELLED
// (DELETE /orders/{id}) or REJECTED (not enough buying power at fill time).
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
	CodeOrderNotFound      = "ORDER_NOT_FOUND"
	CodeOrderNotOpen       = "ORDER_NOT_OPEN"
)

var (
	// ErrInsufficientFunds means a BUY's total exceeds the user's cash. Handlers map it to 422 INSUFFICIENT_FUNDS.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrInsufficientShares means a SELL's quantity exceeds the position. Handlers map it to 422 INSUFFICIENT_SHARES.
	ErrInsufficientShares = errors.New("insufficient shares")
	// ErrIdempotencyKeyReused means the Idempotency-Key was already used by this user for a different
	// order (another orderbook_id, side, quantity or type). Handlers map it to 409 CONFLICT.
	ErrIdempotencyKeyReused = errors.New("idempotency key already used for a different order")
	// ErrOrderNotFound means no order matches (e.g. no order with this idempotency key yet, or the id belongs to
	// another user). Handlers map it to 404 ORDER_NOT_FOUND.
	ErrOrderNotFound = errors.New("order not found")
	// ErrOrderNotOpen means the order already left OPEN (filled, cancelled or rejected), e.g. a cancel that lost
	// the race against a fill. Handlers map it to 409 ORDER_NOT_OPEN.
	ErrOrderNotOpen = errors.New("order is not open")
)

// OrderRequest is the body of POST /orders (#/components/schemas/OrderRequest). The service validates it
// (non-blank orderbook_id, side BUY or SELL, quantity > 0 with at most 4 decimals, type empty, MARKET, LIMIT
// or STOP, limit_price exactly on LIMIT and stop_price exactly on STOP, each > 0 with at most 2 decimals) and
// answers a *ValidationError otherwise. Quantity and the prices accept JSON numbers.
type OrderRequest struct {
	OrderbookID string           `json:"orderbook_id"`
	Side        OrderSide        `json:"side"`
	Quantity    decimal.Decimal  `json:"quantity"`
	Type        OrderType        `json:"type,omitempty"`
	LimitPrice  *decimal.Decimal `json:"limit_price,omitempty"`
	StopPrice   *decimal.Decimal `json:"stop_price,omitempty"`
	Note        string           `json:"note,omitempty"`
}

// Order is an order and, once FILLED, its fill (#/components/schemas/Order). Symbol and Name come from the
// stock row. It marshals money fields as JSON numbers with 2 decimals and Quantity with up to 4 decimals;
// FillPrice, Commission and Total are null on the wire unless Status is FILLED.
type Order struct {
	ID          uint
	OrderbookID string
	Symbol      string
	Name        string
	Side        OrderSide
	Type        OrderType
	Status      OrderStatus
	Quantity    decimal.Decimal
	// LimitPrice is set on LIMIT orders and StopPrice on STOP orders; both are nil otherwise.
	LimitPrice *decimal.Decimal
	StopPrice  *decimal.Decimal
	FillPrice  decimal.Decimal
	Commission decimal.Decimal
	// Total is the cash moved: gross + commission for a BUY (debited), gross − commission for a SELL (credited).
	Total        decimal.Decimal
	Note         string
	RealizedPnL  *decimal.Decimal
	RejectReason *string
	CreatedAt    time.Time
	FilledAt     *time.Time
}

type orderJSON struct {
	ID           uint         `json:"id"`
	OrderbookID  string       `json:"orderbook_id"`
	Symbol       string       `json:"symbol"`
	Name         string       `json:"name"`
	Side         OrderSide    `json:"side"`
	Type         OrderType    `json:"type"`
	Status       OrderStatus  `json:"status"`
	Quantity     json.Number  `json:"quantity"`
	LimitPrice   *json.Number `json:"limit_price"`
	StopPrice    *json.Number `json:"stop_price"`
	FillPrice    *json.Number `json:"fill_price"`
	Commission   *json.Number `json:"commission"`
	Total        *json.Number `json:"total"`
	Note         string       `json:"note"`
	RejectReason *string      `json:"reject_reason"`
	CreatedAt    time.Time    `json:"created_at"`
	FilledAt     *time.Time   `json:"filled_at"`
}

// moneyNumber is d as a JSON number with exactly 2 decimals.
func moneyNumber(d decimal.Decimal) *json.Number {
	n := json.Number(d.StringFixed(2))
	return &n
}

// optionalMoney is moneyNumber(*d), or nil (JSON null) when d is nil.
func optionalMoney(d *decimal.Decimal) *json.Number {
	if d == nil {
		return nil
	}
	return moneyNumber(*d)
}

// MarshalJSON writes the wire shape of #/components/schemas/Order; timestamps are UTC.
func (o Order) MarshalJSON() ([]byte, error) {
	var filledAt *time.Time
	if o.FilledAt != nil {
		t := o.FilledAt.UTC()
		filledAt = &t
	}
	out := orderJSON{
		ID:           o.ID,
		OrderbookID:  o.OrderbookID,
		Symbol:       o.Symbol,
		Name:         o.Name,
		Side:         o.Side,
		Type:         o.Type,
		Status:       o.Status,
		Quantity:     json.Number(o.Quantity.Round(4).String()),
		LimitPrice:   optionalMoney(o.LimitPrice),
		StopPrice:    optionalMoney(o.StopPrice),
		Note:         o.Note,
		RejectReason: o.RejectReason,
		CreatedAt:    o.CreatedAt.UTC(),
		FilledAt:     filledAt,
	}
	if o.Status == OrderStatusFilled {
		out.FillPrice = moneyNumber(o.FillPrice)
		out.Commission = moneyNumber(o.Commission)
		out.Total = moneyNumber(o.Total)
	}
	return json.Marshal(out)
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
	Note  string
	// IdempotencyKey is "" when the request had no Idempotency-Key header (stored as NULL).
	IdempotencyKey string
}

// PendingOrderParams is a validated LIMIT or STOP order for OrderRepository.PlacePending (story NW-08).
type PendingOrderParams struct {
	UserID   uint
	StockID  uint
	Side     OrderSide
	Type     OrderType
	Quantity decimal.Decimal
	// LimitPrice is set for LIMIT and StopPrice for STOP (2 decimals).
	LimitPrice *decimal.Decimal
	StopPrice  *decimal.Decimal
	// ReservedCash is the BUY reservation (see services.ReservationFor); zero for a SELL, which reserves Quantity.
	ReservedCash decimal.Decimal
	Note         string
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
	// created=false and changes nothing. Otherwise it verifies buying power (BUY: balance − reserved_cash >=
	// Total, else ErrInsufficientFunds) or available shares (SELL: quantity − reserved_quantity >= Quantity,
	// else ErrInsufficientShares), moves
	// the cash, updates the position and the realized P&L, inserts a FILLED order with filled_at = now and
	// returns it with created=true. Positions (story NW-05): BUY sets avg_cost = (avg_cost × old quantity +
	// Total) / new quantity, commission included; SELL leaves avg_cost unchanged, adds
	// round2(Total − avg_cost × Quantity) to users.realized_pnl, and deletes the row at zero.
	// A unique-index violation on (user_id, idempotency_key) also yields the existing order, created=false.
	Execute(ctx context.Context, p ExecuteOrderParams) (order *Order, created bool, err error)

	// PlacePending stores a LIMIT or STOP order as OPEN (story NW-08) in one DB transaction with the lock order
	// of Execute (user row, then position): the idempotency key is checked like Execute does (an existing order
	// is returned with created=false); then a BUY needs balance − reserved_cash >= ReservedCash (else
	// ErrInsufficientFunds) and adds it to users.reserved_cash, a SELL needs quantity − reserved_quantity >=
	// Quantity (else ErrInsufficientShares) and adds it to positions.reserved_quantity.
	PlacePending(ctx context.Context, p PendingOrderParams) (order *Order, created bool, err error)

	// GetByID returns the user's order with this id, or ErrOrderNotFound (also when another user owns it).
	GetByID(ctx context.Context, userID, orderID uint) (*Order, error)

	// Cancel moves the user's OPEN order to CANCELLED and releases its reservation, in one DB transaction that
	// locks the user row, the position, then the order (the lock order of the matching engine's fill). It
	// returns ErrOrderNotFound for an unknown or foreign id and ErrOrderNotOpen when the order already left OPEN.
	Cancel(ctx context.Context, userID, orderID uint) (*Order, error)

	// ListByUser returns up to limit of the user's orders, newest first (created_at desc, then id desc).
	// With before set, only orders strictly after that position in this order are returned (keyset
	// pagination); with statuses, only orders in one of them (story NW-08). Symbol and Name come from the
	// stock row (soft-deleted stocks included). It returns an empty, non-nil slice when there are none
	// (story NW-05).
	ListByUser(ctx context.Context, userID uint, before *OrderCursor, limit int, statuses ...OrderStatus) ([]Order, error)

	// ListAllByUser returns all orders of the user, ordered by created_at asc.
	ListAllByUser(ctx context.Context, userID uint) ([]Order, error)

	// UpdateNote updates the note on an order owned by userID (story NW-17).
	UpdateNote(ctx context.Context, userID, orderID uint, note string) (*Order, error)
}
