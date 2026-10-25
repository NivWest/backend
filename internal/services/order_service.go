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

// PlaceOrder validates, prices and executes a market order for userID. idempotencyKey is "" when the
// request had none. created is false when the key replays an earlier order with the same payload: that
// order is returned as it was stored and nothing is executed.
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

// replay returns an order stored under the request's idempotency key, provided it was placed with the
// same orderbook_id, side, quantity and type; otherwise the key was reused for a different order.
func replay(existing *domain.Order, req domain.OrderRequest) (*domain.Order, bool, error) {
	if existing.OrderbookID != req.OrderbookID ||
		existing.Side != req.Side ||
		!existing.Quantity.Equal(req.Quantity) ||
		existing.Type != req.Type {
		return nil, false, domain.ErrIdempotencyKeyReused
	}
	return existing, false, nil
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
	if req.Type != domain.OrderTypeMarket {
		return req, &domain.ValidationError{Msg: "type must be MARKET"}
	}
	return req, nil
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
