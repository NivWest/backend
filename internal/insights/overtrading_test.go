package insights

import (
	"context"
	"testing"
	"time"

	"backend/internal/domain"
)

func TestOvertradingDetector(t *testing.T) {
	d := NewOvertradingDetector()
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		orders        []domain.OrderSummary
		wantTriggered bool
	}{
		{
			name:          "empty history returns false",
			orders:        nil,
			wantTriggered: false,
		},
		{
			name: "exactly 10 orders on 1 day does not trigger",
			orders: func() []domain.OrderSummary {
				var out []domain.OrderSummary
				for i := 1; i <= 10; i++ {
					out = append(out, domain.OrderSummary{
						ID:        uint(i),
						CreatedAt: now.Add(-time.Duration(i) * time.Hour),
					})
				}
				return out
			}(),
			wantTriggered: false,
		},
		{
			name: "11 orders on 1 day triggers",
			orders: func() []domain.OrderSummary {
				var out []domain.OrderSummary
				for i := 1; i <= 11; i++ {
					out = append(out, domain.OrderSummary{
						ID:        uint(i),
						CreatedAt: now.Add(-time.Duration(i) * time.Minute),
					})
				}
				return out
			}(),
			wantTriggered: true,
		},
		{
			name: "orders older than 7 days are ignored",
			orders: func() []domain.OrderSummary {
				var out []domain.OrderSummary
				for i := 1; i <= 20; i++ {
					out = append(out, domain.OrderSummary{
						ID:        uint(i),
						CreatedAt: now.Add(-time.Duration(10+i) * 24 * time.Hour),
					})
				}
				return out
			}(),
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
		})
	}
}
