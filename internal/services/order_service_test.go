package services

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
)

// newTestOrderService builds an OrderService with the simulated latency disabled.
func newTestOrderService(repo domain.OrderRepository, provider domain.StockProvider) *OrderService {
	s := NewOrderService(repo, provider)
	s.sleep = func(time.Duration) {}
	return s
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestOrderService_ExecuteOrder_PricingAndSlippage(t *testing.T) {
	tests := []struct {
		name      string
		orderType string
		quantity  float64
		wantPrice float64
	}{
		{name: "buy at threshold has no slippage", orderType: "BUY", quantity: 100, wantPrice: 200},
		{name: "sell at threshold has no slippage", orderType: "SELL", quantity: 100, wantPrice: 200},
		{name: "high volume buy pays 0.5% more", orderType: "BUY", quantity: 101, wantPrice: 201},
		{name: "high volume sell receives 0.5% less", orderType: "SELL", quantity: 150, wantPrice: 199},
		{name: "fractional quantity", orderType: "BUY", quantity: 0.5, wantPrice: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			provider := &testutil.StockProvider{GetStockQuoteFn: testutil.QuoteJSON(`{"last":200}`)}
			svc := newTestOrderService(repo, provider)

			err := svc.ExecuteOrder(context.Background(), 7, domain.OrderRequest{
				Symbol: "VOLV-B", Name: "Volvo B", Type: tt.orderType, Quantity: tt.quantity,
			})
			if err != nil {
				t.Fatalf("ExecuteOrder() error = %v", err)
			}
			if len(repo.Executed) != 1 {
				t.Fatalf("expected 1 executed transaction, got %d", len(repo.Executed))
			}
			got := repo.Executed[0]
			if got.UserID != 7 || got.StockID != 1 || got.Type != tt.orderType || got.Quantity != tt.quantity {
				t.Errorf("unexpected transaction args: %+v", got)
			}
			if !almostEqual(got.Price, tt.wantPrice) {
				t.Errorf("price = %v, want %v", got.Price, tt.wantPrice)
			}
			if !almostEqual(got.Total, tt.wantPrice*tt.quantity) {
				t.Errorf("total = %v, want %v", got.Total, tt.wantPrice*tt.quantity)
			}
		})
	}
}

func TestOrderService_ExecuteOrder_SimulatesLatency(t *testing.T) {
	var slept time.Duration
	svc := NewOrderService(&testutil.OrderRepo{}, &testutil.StockProvider{GetStockQuoteFn: testutil.QuoteJSON(`{"last":10}`)})
	svc.sleep = func(d time.Duration) { slept = d }

	if err := svc.ExecuteOrder(context.Background(), 1, domain.OrderRequest{Symbol: "X", Type: "BUY", Quantity: 1}); err != nil {
		t.Fatalf("ExecuteOrder() error = %v", err)
	}
	if slept < 50*time.Millisecond || slept >= 500*time.Millisecond {
		t.Errorf("latency = %v, want within [50ms, 500ms)", slept)
	}
}

func TestOrderService_ExecuteOrder_Errors(t *testing.T) {
	repoErr := errors.New("insufficient funds")

	tests := []struct {
		name     string
		quote    func(context.Context, string) ([]byte, error)
		repo     *testutil.OrderRepo
		wantErr  string
		wantExec bool
	}{
		{
			name:    "quote fetch fails",
			quote:   func(context.Context, string) ([]byte, error) { return nil, errors.New("upstream down") },
			repo:    &testutil.OrderRepo{},
			wantErr: "failed to fetch live stock quote for execution",
		},
		{
			name:    "quote is not JSON",
			quote:   testutil.QuoteJSON(`not-json`),
			repo:    &testutil.OrderRepo{},
			wantErr: "failed to parse live stock quote",
		},
		{
			name:    "quote price is zero",
			quote:   testutil.QuoteJSON(`{"last":0}`),
			repo:    &testutil.OrderRepo{},
			wantErr: "invalid stock price received",
		},
		{
			name:    "quote price is negative",
			quote:   testutil.QuoteJSON(`{"last":-5}`),
			repo:    &testutil.OrderRepo{},
			wantErr: "invalid stock price received",
		},
		{
			name:  "stock registration fails",
			quote: testutil.QuoteJSON(`{"last":10}`),
			repo: &testutil.OrderRepo{EnsureStockFn: func(context.Context, string, string) (*models.Stock, error) {
				return nil, errors.New("db down")
			}},
			wantErr: "failed to register stock in database",
		},
		{
			name:  "transaction error is propagated",
			quote: testutil.QuoteJSON(`{"last":10}`),
			repo: &testutil.OrderRepo{ExecuteTransactionFn: func(context.Context, testutil.ExecutedOrder) error {
				return repoErr
			}},
			wantErr: repoErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestOrderService(tt.repo, &testutil.StockProvider{GetStockQuoteFn: tt.quote})
			err := svc.ExecuteOrder(context.Background(), 1, domain.OrderRequest{Symbol: "X", Type: "BUY", Quantity: 1})
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("ExecuteOrder() error = %v, want %q", err, tt.wantErr)
			}
			if len(tt.repo.Executed) != 0 {
				t.Errorf("expected no committed transaction, got %d", len(tt.repo.Executed))
			}
		})
	}
}
