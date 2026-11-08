package insights

import (
	"context"
	"testing"

	"backend/internal/domain"
	"github.com/shopspring/decimal"
)

func TestDispositionEffectDetector(t *testing.T) {
	d := NewDispositionEffectDetector()
	ctx := context.Background()

	winPnL := decimal.NewFromFloat(100.0)
	lossPnL := decimal.NewFromFloat(-100.0)

	tests := []struct {
		name          string
		orders        []domain.OrderSummary
		positions     []domain.PositionSnapshot
		wantTriggered bool
	}{
		{
			name:          "empty history returns false",
			orders:        nil,
			positions:     nil,
			wantTriggered: false,
		},
		{
			name: "only 3 sells does not trigger even with high ratio",
			orders: []domain.OrderSummary{
				{ID: 1, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 2, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 3, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
			},
			positions:     nil,
			wantTriggered: false,
		},
		{
			name: "4 winning sells and no losses triggers (diff 1.0 > 0.20)",
			orders: []domain.OrderSummary{
				{ID: 1, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 2, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 3, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 4, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
			},
			positions:     nil,
			wantTriggered: true,
		},
		{
			name: "4 sells balanced between wins and losses does not trigger (diff 0.0 <= 0.20)",
			orders: []domain.OrderSummary{
				{ID: 1, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 2, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 3, Side: domain.OrderSideSell, RealizedPnL: &lossPnL},
				{ID: 4, Side: domain.OrderSideSell, RealizedPnL: &lossPnL},
			},
			positions:     nil,
			wantTriggered: false,
		},
		{
			name: "holding losing positions while realizing gains triggers disposition effect",
			orders: []domain.OrderSummary{
				{ID: 1, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 2, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 3, Side: domain.OrderSideSell, RealizedPnL: &winPnL},
				{ID: 4, Side: domain.OrderSideSell, RealizedPnL: &lossPnL},
			},
			positions: []domain.PositionSnapshot{
				{StockID: 10, UnrealizedPnL: decimal.NewFromFloat(-50.0)},
				{StockID: 11, UnrealizedPnL: decimal.NewFromFloat(-150.0)},
			},
			// RG=3, PG=0 -> PGR = 3/3 = 1.0
			// RL=1, PL=2 -> PLR = 1/3 = 0.333
			// diff = 1.0 - 0.333 = 0.667 > 0.20 -> true
			wantTriggered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := d.Evaluate(ctx, domain.TradeHistory{
				Orders:    tt.orders,
				Positions: tt.positions,
			})
			if res.Triggered != tt.wantTriggered {
				t.Fatalf("Evaluate().Triggered = %v, want %v", res.Triggered, tt.wantTriggered)
			}
		})
	}
}
