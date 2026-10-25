package services

import (
	"testing"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestPriceOrder(t *testing.T) {
	tests := []struct {
		name     string
		side     domain.OrderSide
		quantity string
		bid, ask string
		// want: fill price, gross, commission, total
		fill, gross, commission, total string
	}{
		{
			name: "AC1 buy fills at the ask", side: domain.OrderSideBuy, quantity: "10", bid: "99.90", ask: "100.10",
			fill: "100.10", gross: "1001.00", commission: "2.50", total: "1003.50",
		},
		{
			name: "AC2 sell fills at the bid with the minimum commission", side: domain.OrderSideSell, quantity: "4", bid: "99.90", ask: "100.10",
			fill: "99.90", gross: "399.60", commission: "1.00", total: "398.60",
		},
		{
			name: "AC5 buy 200 pays 0.5% slippage", side: domain.OrderSideBuy, quantity: "200", bid: "99.90", ask: "100.10",
			fill: "100.60", gross: "20120.00", commission: "50.30", total: "20170.30",
		},
		{
			name: "quantity exactly 100 has no slippage", side: domain.OrderSideBuy, quantity: "100", bid: "99.90", ask: "100.10",
			fill: "100.10", gross: "10010.00", commission: "25.03", total: "10035.03",
		},
		{
			name: "quantity 101 has slippage", side: domain.OrderSideBuy, quantity: "101", bid: "99.90", ask: "100.10",
			fill: "100.60", gross: "10160.60", commission: "25.40", total: "10186.00",
		},
		{
			name: "sell 200 receives 0.5% less", side: domain.OrderSideSell, quantity: "200", bid: "99.90", ask: "100.10",
			fill: "99.40", gross: "19880.00", commission: "49.70", total: "19830.30",
		},
		{
			name: "fill price rounds half-up before anything else", side: domain.OrderSideBuy, quantity: "101", bid: "0.99", ask: "1.00",
			fill: "1.01", gross: "102.01", commission: "1.00", total: "103.01",
		},
		{
			name: "fractional quantity", side: domain.OrderSideBuy, quantity: "0.5", bid: "99.90", ask: "100.10",
			fill: "100.10", gross: "50.05", commission: "1.00", total: "51.05",
		},
		{
			name: "gross rounds half-up", side: domain.OrderSideBuy, quantity: "0.0005", bid: "10.00", ask: "10.10",
			fill: "10.10", gross: "0.01", commission: "1.00", total: "1.01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PriceOrder(tt.side, dec(tt.quantity), dec(tt.bid), dec(tt.ask))
			check := func(field string, got decimal.Decimal, want string) {
				t.Helper()
				if !got.Equal(dec(want)) {
					t.Errorf("%s = %s, want %s", field, got, want)
				}
			}
			check("FillPrice", got.FillPrice, tt.fill)
			check("Gross", got.Gross, tt.gross)
			check("Commission", got.Commission, tt.commission)
			check("Total", got.Total, tt.total)
		})
	}
}
