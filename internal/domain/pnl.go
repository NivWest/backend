package domain

import "github.com/shopspring/decimal"

// Cost basis and realized P&L (story NW-05). Pure decimal math shared by the order repository (which
// stores the results) and anything that needs to reproduce them. Rounding is half-up (half away from
// zero for negative values, as decimal.Round), like every other money edge in this codebase.

// AvgCostPlaces is the scale of positions.avg_cost (numeric(20,8)).
const AvgCostPlaces = 8

// NextAvgCost returns the cost basis per share after a BUY:
//
//	(oldAvgCost × oldQty + buyTotal) / (oldQty + buyQty)
//
// rounded half-up to 8 decimals. buyTotal is the cash debited for the BUY, commission included, so the
// commission becomes part of the cost basis. A fresh lot has oldQty 0 (and any oldAvgCost), which gives
// buyTotal / buyQty. It returns zero when the resulting quantity is not positive.
func NextAvgCost(oldQty, oldAvgCost, buyQty, buyTotal decimal.Decimal) decimal.Decimal {
	newQty := oldQty.Add(buyQty)
	if !newQty.IsPositive() {
		return decimal.Zero
	}
	cost := oldAvgCost.Mul(oldQty).Add(buyTotal)
	return cost.DivRound(newQty, AvgCostPlaces)
}

// RealizedPnL returns the profit (negative: loss) a SELL realizes:
//
//	sellTotal − avgCost × qty
//
// rounded half-up to 2 decimals. sellTotal is the cash credited for the SELL (gross − commission), avgCost
// the position's cost basis per share (commission included) and qty the quantity sold.
func RealizedPnL(sellTotal, avgCost, qty decimal.Decimal) decimal.Decimal {
	return sellTotal.Sub(avgCost.Mul(qty)).Round(2)
}
