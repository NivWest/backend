package insights

import (
	"context"
	"testing"
	"time"

	"backend/internal/domain"
	"github.com/shopspring/decimal"
)

func TestRevengeTradingDetector(t *testing.T) {
	d := NewRevengeTradingDetector()
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	negPnL := decimal.NewFromFloat(-100.0)
	posPnL := decimal.NewFromFloat(50.0)

	tests := []struct {
		name          string
		orders        []domain.OrderSummary
		wantTriggered bool
		wantOrderIDs  []uint
	}{
		{
			name:          "empty history returns false",
			orders:        nil,
			wantTriggered: false,
		},
		{
			name: "profitable sell followed by quick buy does not trigger",
			orders: []domain.OrderSummary{
				{ID: 1, Side: domain.OrderSideSell, RealizedPnL: &posPnL, CreatedAt: now},
				{ID: 2, Side: domain.OrderSideBuy, CreatedAt: now.Add(2 * time.Minute)},
			},
			wantTriggered: false,
		},
		{
			name: "loss sell followed by buy after 15 minutes does not trigger",
			orders: []domain.OrderSummary{
				{ID: 1, Side: domain.OrderSideSell, RealizedPnL: &negPnL, CreatedAt: now},
				{ID: 2, Side: domain.OrderSideBuy, CreatedAt: now.Add(15 * time.Minute)},
			},
			wantTriggered: false,
		},
		{
			name: "loss sell followed by buy within 2 minutes triggers",
			orders: []domain.OrderSummary{
				{ID: 10, Side: domain.OrderSideSell, RealizedPnL: &negPnL, CreatedAt: now},
				{ID: 11, Side: domain.OrderSideBuy, CreatedAt: now.Add(2 * time.Minute)},
			},
			wantTriggered: true,
			wantOrderIDs:  []uint{10, 11},
		},
		{
			name: "loss sell followed by another sell does not trigger",
			orders: []domain.OrderSummary{
				{ID: 10, Side: domain.OrderSideSell, RealizedPnL: &negPnL, CreatedAt: now},
				{ID: 11, Side: domain.OrderSideSell, RealizedPnL: &negPnL, CreatedAt: now.Add(2 * time.Minute)},
			},
			wantTriggered: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := d.Evaluate(ctx, domain.TradeHistory{
				Now:    now,
				Orders: tt.orders,
			})
			if res.Triggered != tt.wantTriggered {
				t.Fatalf("Evaluate().Triggered = %v, want %v", res.Triggered, tt.wantTriggered)
			}
			if tt.wantTriggered {
				if len(res.Evidence.OrderIDs) != len(tt.wantOrderIDs) {
					t.Fatalf("Evaluate().Evidence.OrderIDs = %v, want %v", res.Evidence.OrderIDs, tt.wantOrderIDs)
				}
				for i, id := range tt.wantOrderIDs {
					if res.Evidence.OrderIDs[i] != id {
						t.Errorf("OrderIDs[%d] = %d, want %d", i, res.Evidence.OrderIDs[i], id)
					}
				}
			}
		})
	}
}
