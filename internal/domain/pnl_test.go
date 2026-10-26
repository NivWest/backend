package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestNextAvgCost(t *testing.T) {
	tests := []struct {
		name                             string
		oldQty, oldAvg, buyQty, buyTotal string
		want                             string
	}{
		{name: "story: fresh lot, 10 for 1003.50", oldQty: "0", oldAvg: "0", buyQty: "10", buyTotal: "1003.50", want: "100.35"},
		{name: "fresh lot ignores a stale avg cost", oldQty: "0", oldAvg: "123.45", buyQty: "4", buyTotal: "402.00", want: "100.5"},
		{name: "second buy at a different price", oldQty: "10", oldAvg: "100.35", buyQty: "10", buyTotal: "1202.75", want: "110.3125"},
		// (100.35 × 10 + 333.33) / 13 = 1336.83 / 13 = 102.83307692...
		{name: "second buy rounds to 8 decimals", oldQty: "10", oldAvg: "100.35", buyQty: "3", buyTotal: "333.33", want: "102.83307692"},
		// 1/3 → 0.33333333; 2/3 → 0.66666667 (half-up).
		{name: "rounds half-up down", oldQty: "0", oldAvg: "0", buyQty: "3", buyTotal: "1", want: "0.33333333"},
		{name: "rounds half-up up", oldQty: "0", oldAvg: "0", buyQty: "3", buyTotal: "2", want: "0.66666667"},
		// 0.5 × 200 + 25.0625 = 125.0625 over 0.75 shares = 166.75.
		{name: "fractional quantities", oldQty: "0.5", oldAvg: "200", buyQty: "0.25", buyTotal: "25.0625", want: "166.75"},
		{name: "fractional fresh lot", oldQty: "0", oldAvg: "0", buyQty: "0.3333", buyTotal: "33.66", want: "100.99009901"},
		{name: "no quantity", oldQty: "0", oldAvg: "0", buyQty: "0", buyTotal: "0", want: "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextAvgCost(d(tt.oldQty), d(tt.oldAvg), d(tt.buyQty), d(tt.buyTotal))
			if !got.Equal(d(tt.want)) {
				t.Fatalf("NextAvgCost(%s, %s, %s, %s) = %s, want %s", tt.oldQty, tt.oldAvg, tt.buyQty, tt.buyTotal, got, tt.want)
			}
		})
	}
}

func TestRealizedPnL(t *testing.T) {
	tests := []struct {
		name                string
		sellTotal, avg, qty string
		want                string
	}{
		// AC2: sell 5 at bid 109.89, gross 549.45, commission 1.37, total 548.08; 548.08 − 501.75.
		{name: "story: partial sell at a profit", sellTotal: "548.08", avg: "100.35", qty: "5", want: "46.33"},
		{name: "loss", sellTotal: "398.60", avg: "100.35", qty: "4", want: "-2.80"},
		{name: "break even", sellTotal: "501.75", avg: "100.35", qty: "5", want: "0"},
		// 3 × 102.83307692 = 308.49923076 → 400 − that = 91.50076924 → 91.50.
		{name: "8-decimal avg cost rounds to cents", sellTotal: "400.00", avg: "102.83307692", qty: "3", want: "91.50"},
		// 166.75 × 0.25 = 41.6875; 50.00 − 41.6875 = 8.3125 → 8.31.
		{name: "fractional quantity", sellTotal: "50.00", avg: "166.75", qty: "0.25", want: "8.31"},
		// 10.00 − 0.995 × 1 = 9.005 → 9.01.
		{name: "half-up on a profit", sellTotal: "10.00", avg: "0.995", qty: "1", want: "9.01"},
		// 0.99 − 0.995 × 1 = −0.005 → −0.01 (half away from zero).
		{name: "half away from zero on a loss", sellTotal: "0.99", avg: "0.995", qty: "1", want: "-0.01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RealizedPnL(d(tt.sellTotal), d(tt.avg), d(tt.qty))
			if !got.Equal(d(tt.want)) {
				t.Fatalf("RealizedPnL(%s, %s, %s) = %s, want %s", tt.sellTotal, tt.avg, tt.qty, got, tt.want)
			}
		})
	}
}

// TestStoryLedger walks AC1/AC2 end to end through both helpers.
func TestStoryLedger(t *testing.T) {
	avg := NextAvgCost(decimal.Zero, decimal.Zero, d("10"), d("1003.50"))
	if !avg.Equal(d("100.35")) {
		t.Fatalf("avg cost = %s, want 100.35", avg)
	}
	if pnl := RealizedPnL(d("548.08"), avg, d("5")); !pnl.Equal(d("46.33")) {
		t.Fatalf("realized = %s, want 46.33", pnl)
	}
}
