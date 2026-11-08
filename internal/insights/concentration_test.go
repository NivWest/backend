package insights

import (
	"context"
	"testing"

	"backend/internal/domain"
	"github.com/shopspring/decimal"
)

func TestConcentrationDetector(t *testing.T) {
	d := NewConcentrationDetector()
	ctx := context.Background()

	tests := []struct {
		name          string
		totalEquity   decimal.Decimal
		positions     []domain.PositionSnapshot
		orders        []domain.OrderSummary
		wantTriggered bool
		wantOrders    []uint
	}{
		{
			name:          "empty history returns false",
			totalEquity:   decimal.Zero,
			positions:     nil,
			orders:        nil,
			wantTriggered: false,
		},
		{
			name:        "below threshold at 40 percent returns false",
			totalEquity: decimal.NewFromInt(100000),
			positions: []domain.PositionSnapshot{
				{StockID: 1, MarketValue: decimal.NewFromInt(40000)},
				{StockID: 2, MarketValue: decimal.NewFromInt(30000)},
			},
			orders:        []domain.OrderSummary{{ID: 10, StockID: 1}},
			wantTriggered: false,
		},
		{
			name:        "above 40 percent (50 percent) returns true with order ids",
			totalEquity: decimal.NewFromInt(100000),
			positions: []domain.PositionSnapshot{
				{StockID: 1, MarketValue: decimal.NewFromInt(50000)},
				{StockID: 2, MarketValue: decimal.NewFromInt(20000)},
			},
			orders: []domain.OrderSummary{
				{ID: 101, StockID: 1},
				{ID: 102, StockID: 2},
				{ID: 103, StockID: 1},
			},
			wantTriggered: true,
			wantOrders:    []uint{101, 103},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := d.Evaluate(ctx, domain.TradeHistory{
				TotalEquity: tt.totalEquity,
				Positions:   tt.positions,
				Orders:      tt.orders,
			})
			if res.Triggered != tt.wantTriggered {
				t.Fatalf("Evaluate().Triggered = %v, want %v", res.Triggered, tt.wantTriggered)
			}
			if tt.wantTriggered {
				if len(res.Evidence.OrderIDs) != len(tt.wantOrders) {
					t.Fatalf("Evaluate().Evidence.OrderIDs = %v, want %v", res.Evidence.OrderIDs, tt.wantOrders)
				}
				for i, id := range tt.wantOrders {
					if res.Evidence.OrderIDs[i] != id {
						t.Errorf("OrderIDs[%d] = %d, want %d", i, res.Evidence.OrderIDs[i], id)
					}
				}
			}
		})
	}
}
