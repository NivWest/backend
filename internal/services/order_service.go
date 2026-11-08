package services

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

// quantityPlaces is the most decimals an order quantity may have (the column is numeric(18,4)).
const quantityPlaces = 4

// pricePlaces is the most decimals a limit_price or stop_price may have (SEK, numeric(18,2)).
const pricePlaces = 2

// Simulated execution latency: a uniformly random wait in [minLatency, maxLatency).
const (
	minLatency = 50 * time.Millisecond
	maxLatency = 500 * time.Millisecond
)

type OrderService struct {
	repo     domain.OrderRepository
	provider domain.MarketDataProvider
	// sleep simulates execution latency. It is a field so tests can replace it.
	sleep func(time.Duration)
}

func NewOrderService(repo domain.OrderRepository, provider domain.MarketDataProvider) *OrderService {
	return &OrderService{
		repo:     repo,
		provider: provider,
		sleep:    time.Sleep,
	}
}

// SetSleep replaces the function that simulates execution latency (time.Sleep by default), e.g. with a
// no-op in handler tests. It must be called before the service is used.
func (s *OrderService) SetSleep(sleep func(time.Duration)) {
	s.sleep = sleep
}

// PlaceOrder validates and places an order for userID: a MARKET order is priced and executed at once, a LIMIT
// or STOP order is stored OPEN with its reservation (story NW-08) for the matching engine. idempotencyKey is ""
// when the request had none. created is false when the key replays an earlier order with the same payload:
// that order is returned as it was stored and nothing is executed or reserved.
//
// Errors: *domain.ValidationError, domain.ErrIdempotencyKeyReused, domain.ErrInstrumentNotFound,
// domain.ErrMarketDataUnavailable (wrapped), and whatever the repository returns
// (domain.ErrInsufficientFunds, domain.ErrInsufficientShares, …). All stay errors.Is/As-able.
func (s *OrderService) PlaceOrder(ctx context.Context, userID uint, idempotencyKey string, req domain.OrderRequest) (*domain.Order, bool, error) {
	req, err := normalizeOrderRequest(req)
	if err != nil {
		return nil, false, err
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return nil, false, err
	}

	// A retry with a known key is answered from the stored order: no latency, no market data, no execution.
	if idempotencyKey != "" {
		existing, err := s.repo.FindByIdempotencyKey(ctx, userID, idempotencyKey)
		switch {
		case err == nil:
			return replay(existing, req)
		case !errors.Is(err, domain.ErrOrderNotFound):
			return nil, false, fmt.Errorf("find order by idempotency key: %w", err)
		}
	}

	if req.Type.IsPending() {
		return s.placePending(ctx, userID, idempotencyKey, req)
	}

	// Friction: simulated execution latency before the price is read.
	s.sleep(minLatency + time.Duration(rand.Int63n(int64(maxLatency-minLatency))))

	details, err := s.provider.Details(ctx, req.OrderbookID)
	if err != nil {
		return nil, false, marketDataError(err)
	}
	quote, err := s.provider.Quote(ctx, req.OrderbookID)
	if err != nil {
		return nil, false, marketDataError(err)
	}
	if quote.Bid <= 0 || quote.Ask <= 0 {
		return nil, false, fmt.Errorf("%w: no valid bid/ask for %s (bid %v, ask %v)",
			domain.ErrMarketDataUnavailable, req.OrderbookID, quote.Bid, quote.Ask)
	}

	pricing := PriceOrder(req.Side, req.Quantity, decimal.NewFromFloat(quote.Bid), decimal.NewFromFloat(quote.Ask))

	stockID, err := s.repo.EnsureStock(ctx, req.OrderbookID, details.Symbol, details.Name)
	if err != nil {
		return nil, false, fmt.Errorf("ensure stock %s: %w", req.OrderbookID, err)
	}

	order, created, err := s.repo.Execute(ctx, domain.ExecuteOrderParams{
		UserID:         userID,
		StockID:        stockID,
		Side:           req.Side,
		Type:           req.Type,
		Quantity:       req.Quantity,
		FillPrice:      pricing.FillPrice,
		Commission:     pricing.Commission,
		Total:          pricing.Total,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, false, err
	}
	if !created {
		// A concurrent retry with the same key won the race: answer like a replay.
		return replay(order, req)
	}
	return order, true, nil
}

// placePending stores a validated LIMIT or STOP order OPEN. It needs no quote (nothing executes now), only the
// instrument's details for the stock row, so an unknown instrument is still a 404. The BUY reservation is
// ReservationFor; the repository checks it against buying power (or the shares against available shares).
func (s *OrderService) placePending(ctx context.Context, userID uint, idempotencyKey string, req domain.OrderRequest) (*domain.Order, bool, error) {
	details, err := s.provider.Details(ctx, req.OrderbookID)
	if err != nil {
		return nil, false, marketDataError(err)
	}
	stockID, err := s.repo.EnsureStock(ctx, req.OrderbookID, details.Symbol, details.Name)
	if err != nil {
		return nil, false, fmt.Errorf("ensure stock %s: %w", req.OrderbookID, err)
	}

	order, created, err := s.repo.PlacePending(ctx, domain.PendingOrderParams{
		UserID:         userID,
		StockID:        stockID,
		Side:           req.Side,
		Type:           req.Type,
		Quantity:       req.Quantity,
		LimitPrice:     req.LimitPrice,
		StopPrice:      req.StopPrice,
		ReservedCash:   ReservationFor(req.Side, req.Type, req.Quantity, req.LimitPrice, req.StopPrice),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, false, err
	}
	if !created {
		return replay(order, req)
	}
	return order, true, nil
}

// GetOrder returns one of userID's orders. Errors: domain.ErrOrderNotFound (also for another user's order),
// otherwise whatever the repository returns.
func (s *OrderService) GetOrder(ctx context.Context, userID, orderID uint) (*domain.Order, error) {
	return s.repo.GetByID(ctx, userID, orderID)
}

// CancelOrder cancels one of userID's OPEN orders and releases its reservation. Errors:
// domain.ErrOrderNotFound (unknown or another user's), domain.ErrOrderNotOpen (already filled, cancelled or
// rejected; also the loser of a cancel-vs-fill race), otherwise whatever the repository returns.
func (s *OrderService) CancelOrder(ctx context.Context, userID, orderID uint) (*domain.Order, error) {
	return s.repo.Cancel(ctx, userID, orderID)
}

// ListOrders returns one page of userID's orders, newest first (created_at desc, then id desc). limit
// must be 1..domain.MaxOrderPageLimit; cursor is "" for the first page or a next_cursor from an earlier
// page. With statuses, only orders in one of them are listed (story NW-08). Items is never nil; NextCursor is
// nil on the last page.
//
// Errors: *domain.ValidationError for a bad limit, cursor or status, otherwise whatever the repository returns.
func (s *OrderService) ListOrders(ctx context.Context, userID uint, limit int, cursor string, statuses ...domain.OrderStatus) (*domain.OrderPage, error) {
	if limit < 1 || limit > domain.MaxOrderPageLimit {
		return nil, &domain.ValidationError{Msg: fmt.Sprintf("limit must be an integer from 1 to %d", domain.MaxOrderPageLimit)}
	}
	var before *domain.OrderCursor
	if cursor != "" {
		c, err := decodeOrderCursor(cursor)
		if err != nil {
			return nil, err
		}
		before = &c
	}
	for _, st := range statuses {
		switch st {
		case domain.OrderStatusOpen, domain.OrderStatusFilled, domain.OrderStatusCancelled, domain.OrderStatusRejected:
		default:
			return nil, &domain.ValidationError{Msg: "status must be a comma-separated list of OPEN, FILLED, CANCELLED, REJECTED"}
		}
	}

	// One extra row tells whether another page follows without a separate count query.
	orders, err := s.repo.ListByUser(ctx, userID, before, limit+1, statuses...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}

	page := &domain.OrderPage{Items: make([]domain.Order, 0, min(len(orders), limit))}
	if len(orders) > limit {
		orders = orders[:limit]
		last := orders[limit-1]
		next := encodeOrderCursor(domain.OrderCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		page.NextCursor = &next
	}
	page.Items = append(page.Items, orders...)
	return page, nil
}

// replay returns an order stored under the request's idempotency key, provided it was placed with the
// same orderbook_id, side, quantity, type and limit/stop price; otherwise the key was reused for a different order.
func replay(existing *domain.Order, req domain.OrderRequest) (*domain.Order, bool, error) {
	if existing.OrderbookID != req.OrderbookID ||
		existing.Side != req.Side ||
		!existing.Quantity.Equal(req.Quantity) ||
		existing.Type != req.Type ||
		!samePrice(existing.LimitPrice, req.LimitPrice) ||
		!samePrice(existing.StopPrice, req.StopPrice) {
		return nil, false, domain.ErrIdempotencyKeyReused
	}
	return existing, false, nil
}

// samePrice reports whether two optional prices are both absent or numerically equal.
func samePrice(a, b *decimal.Decimal) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// normalizeOrderRequest trims the orderbook id, defaults the type to MARKET and validates the request.
func normalizeOrderRequest(req domain.OrderRequest) (domain.OrderRequest, error) {
	req.OrderbookID = strings.TrimSpace(req.OrderbookID)
	if req.OrderbookID == "" {
		return req, &domain.ValidationError{Msg: "orderbook_id is required"}
	}
	if req.Side != domain.OrderSideBuy && req.Side != domain.OrderSideSell {
		return req, &domain.ValidationError{Msg: "side must be BUY or SELL"}
	}
	if !req.Quantity.IsPositive() {
		return req, &domain.ValidationError{Msg: "quantity must be greater than 0"}
	}
	if !req.Quantity.Equal(req.Quantity.Truncate(quantityPlaces)) {
		return req, &domain.ValidationError{Msg: "quantity must have at most 4 decimals"}
	}
	if req.Type == "" {
		req.Type = domain.OrderTypeMarket
	}
	switch req.Type {
	case domain.OrderTypeMarket:
		if req.LimitPrice != nil || req.StopPrice != nil {
			return req, &domain.ValidationError{Msg: "limit_price and stop_price are not allowed on a MARKET order"}
		}
	case domain.OrderTypeLimit:
		if req.StopPrice != nil {
			return req, &domain.ValidationError{Msg: "stop_price is not allowed on a LIMIT order"}
		}
		if err := validatePrice("limit_price", "LIMIT", req.LimitPrice); err != nil {
			return req, err
		}
	case domain.OrderTypeStop:
		if req.LimitPrice != nil {
			return req, &domain.ValidationError{Msg: "limit_price is not allowed on a STOP order"}
		}
		if err := validatePrice("stop_price", "STOP", req.StopPrice); err != nil {
			return req, err
		}
	default:
		return req, &domain.ValidationError{Msg: "type must be MARKET, LIMIT or STOP"}
	}
	return req, nil
}

// validatePrice requires a limit or stop price that is > 0 with at most 2 decimals.
func validatePrice(field, orderType string, price *decimal.Decimal) error {
	switch {
	case price == nil:
		return &domain.ValidationError{Msg: fmt.Sprintf("%s is required for a %s order", field, orderType)}
	case !price.IsPositive():
		return &domain.ValidationError{Msg: field + " must be greater than 0"}
	case !price.Equal(price.Truncate(pricePlaces)):
		return &domain.ValidationError{Msg: field + " must have at most 2 decimals"}
	}
	return nil
}

// validateIdempotencyKey accepts "" (no key) or 1–MaxIdempotencyKeyLength printable ASCII characters (0x21–0x7E).
func validateIdempotencyKey(key string) error {
	if len(key) > domain.MaxIdempotencyKeyLength {
		return &domain.ValidationError{Msg: fmt.Sprintf("%s must be at most %d characters", domain.IdempotencyKeyHeader, domain.MaxIdempotencyKeyLength)}
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x21 || key[i] > 0x7E {
			return &domain.ValidationError{Msg: domain.IdempotencyKeyHeader + " must be printable ASCII without spaces"}
		}
	}
	return nil
}

// marketDataError lets ErrInstrumentNotFound (404) through and turns every other provider failure into
// an error wrapping ErrMarketDataUnavailable (502).
func marketDataError(err error) error {
	if errors.Is(err, domain.ErrInstrumentNotFound) || errors.Is(err, domain.ErrMarketDataUnavailable) {
		return err
	}
	return fmt.Errorf("%w: %v", domain.ErrMarketDataUnavailable, err)
}
