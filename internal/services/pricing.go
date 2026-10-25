package services

import (
	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

// Market order pricing (story NW-04, api/openapi.yaml#/components/schemas/Order). All money is SEK.
var (
	// SlippageThreshold is the quantity above which an order moves the price against the trader.
	SlippageThreshold = decimal.NewFromInt(100)
	// SlippageRate is the price penalty for orders above SlippageThreshold (0.5%).
	SlippageRate = decimal.RequireFromString("0.005")
	// CommissionRate is the brokerage fee as a share of the gross amount (0.25%).
	CommissionRate = decimal.RequireFromString("0.0025")
	// MinimumCommission is the smallest fee charged per order.
	MinimumCommission = decimal.RequireFromString("1.00")
)

// moneyPlaces is the number of decimals money is rounded to (half-up).
const moneyPlaces = 2

// OrderPricing is the priced fill of a market order. Every field is rounded half-up to 2 decimals.
type OrderPricing struct {
	FillPrice  decimal.Decimal
	Gross      decimal.Decimal
	Commission decimal.Decimal
	// Total is the cash moved: Gross + Commission for a BUY (debited), Gross - Commission for a SELL (credited).
	Total decimal.Decimal
}

// PriceOrder prices a market order of quantity shares from the current bid and ask:
//
//   - base price = ask for BUY, bid for SELL;
//   - quantity > SlippageThreshold applies SlippageRate against the trader (BUY ×1.005, SELL ×0.995);
//   - FillPrice = base price rounded to 2 decimals, before anything else is computed;
//   - Gross = FillPrice × quantity, Commission = max(MinimumCommission, CommissionRate × Gross);
//   - Total = Gross + Commission (BUY) or Gross − Commission (SELL).
//
// Rounding is half-up (decimal.Round rounds half away from zero, and every input here is positive).
func PriceOrder(side domain.OrderSide, quantity, bid, ask decimal.Decimal) OrderPricing {
	buy := side == domain.OrderSideBuy

	base := bid
	if buy {
		base = ask
	}
	if quantity.GreaterThan(SlippageThreshold) {
		factor := decimal.NewFromInt(1).Sub(SlippageRate)
		if buy {
			factor = decimal.NewFromInt(1).Add(SlippageRate)
		}
		base = base.Mul(factor)
	}

	fillPrice := base.Round(moneyPlaces)
	gross := fillPrice.Mul(quantity).Round(moneyPlaces)
	commission := decimal.Max(MinimumCommission, CommissionRate.Mul(gross)).Round(moneyPlaces)

	total := gross.Sub(commission)
	if buy {
		total = gross.Add(commission)
	}

	return OrderPricing{FillPrice: fillPrice, Gross: gross, Commission: commission, Total: total}
}
