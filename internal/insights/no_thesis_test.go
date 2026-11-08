package insights

import (
	"context"
	"testing"
	"time"

	"backend/internal/domain"
)

func TestNoThesisDetector(t *testing.T) {
	d := NewNoThesisDetector()
	ctx := context.Background()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		orders        []domain.OrderSummary
		wantTriggered bool
		wantOrderIDs  []uint
	}{
		{
			name:          "empty history returns false (AC4 requirement)",
			orders:        nil,
			wantTriggered: false,
		},
		{
			name: "5 of 10 without note (50 percent) does not trigger",
			orders: func() []domain.OrderSummary {
				var out []domain.OrderSummary
				for i := 1; i <= 10; i++ {
					note := "thesis"
					if i <= 5 {
						note = ""
					}
					out = append(out, domain.OrderSummary{
						ID:        uint(i),
						Note:      note,
						CreatedAt: now.Add(time.Duration(i) * time.Minute),
					})
				}
				return out
			}(),
			wantTriggered: false,
		},
		{
			name: "6 of 10 without note (60 percent) triggers",
			orders: func() []domain.OrderSummary {
				var out []domain.OrderSummary
				for i := 1; i <= 10; i++ {
					note := "thesis"
					if i <= 6 {
						note = ""
					}
					out = append(out, domain.OrderSummary{
						ID:        uint(i),
						Note:      note,
						CreatedAt: now.Add(time.Duration(i) * time.Minute),
					})
				}
				return out
			}(),
			wantTriggered: true,
		},
		{
			name: "single order with no note triggers (100 percent)",
			orders: []domain.OrderSummary{
				{ID: 42, Note: "", CreatedAt: now},
			},
			wantTriggered: true,
			wantOrderIDs:  []uint{42},
		},
		{
			name: "single order with note does not trigger (0 percent)",
			orders: []domain.OrderSummary{
				{ID: 42, Note: "Valid thesis", CreatedAt: now},
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
			if tt.wantTriggered && len(tt.wantOrderIDs) > 0 {
				if len(res.Evidence.OrderIDs) != len(tt.wantOrderIDs) {
					t.Fatalf("OrderIDs count = %d, want %d", len(res.Evidence.OrderIDs), len(tt.wantOrderIDs))
				}
			}
		})
	}
}
