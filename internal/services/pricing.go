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

// StopReservationBuffer is applied to stop_price for a BUY STOP reservation: the order fills at the ask once the
// price has risen through the stop, so it reserves for a fill up to 5% above it.
var StopReservationBuffer = decimal.RequireFromString("1.05")

// ReservationFor is the cash an OPEN BUY order reserves at placement (story NW-08), rounded half-up to 2 decimals:
//
//   - BUY LIMIT: price = limit_price;
//   - BUY STOP: price = round2(stop_price × StopReservationBuffer);
//   - gross = round2(quantity × price), commission = max(MinimumCommission, round2(CommissionRate × gross));
//   - reservation = gross + commission.
//
// SELL orders reserve shares, not cash, so they (and a nil price) return zero.
func ReservationFor(side domain.OrderSide, orderType domain.OrderType, quantity decimal.Decimal, limitPrice, stopPrice *decimal.Decimal) decimal.Decimal {
	if side != domain.OrderSideBuy {
		return decimal.Zero
	}
	var price decimal.Decimal
	switch {
	case orderType == domain.OrderTypeLimit && limitPrice != nil:
		price = *limitPrice
	case orderType == domain.OrderTypeStop && stopPrice != nil:
		price = stopPrice.Mul(StopReservationBuffer).Round(moneyPlaces)
	default:
		return decimal.Zero
	}
	gross := quantity.Mul(price).Round(moneyPlaces)
	commission := decimal.Max(MinimumCommission, CommissionRate.Mul(gross)).Round(moneyPlaces)
	return gross.Add(commission)
}
