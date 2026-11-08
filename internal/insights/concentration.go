package insights

import (
	"context"

	"backend/internal/domain"
	"github.com/shopspring/decimal"
)

// ConcentrationDetector triggers when any single position exceeds 40% of total equity.
type ConcentrationDetector struct{}

func NewConcentrationDetector() domain.Detector {
	return &ConcentrationDetector{}
}

func (d *ConcentrationDetector) ID() domain.DetectorID {
	return domain.DetectorConcentration
}

func (d *ConcentrationDetector) Title() string {
	return "Position concentration"
}

func (d *ConcentrationDetector) Evaluate(_ context.Context, history domain.TradeHistory) domain.DetectorResult {
	res := domain.DetectorResult{
		ID:          d.ID(),
		Title:       d.Title(),
		Triggered:   false,
		Severity:    "warning",
		Evidence:    domain.DetectorEvidence{OrderIDs: []uint{}},
		Explanation: "A single position makes up more than 40% of your portfolio equity, creating concentrated risk.",
	}

	if !history.TotalEquity.IsPositive() || len(history.Positions) == 0 {
		return res
	}

	threshold := decimal.NewFromFloat(0.40)
	stockMap := make(map[uint]bool)
	orderbookMap := make(map[string]bool)
	triggered := false
	highestWeight := 0.0

	for _, pos := range history.Positions {
		if !pos.MarketValue.IsPositive() {
			continue
		}
		weightRatio := pos.MarketValue.Div(history.TotalEquity)
		weightPct, _ := weightRatio.Mul(decimal.NewFromInt(100)).Float64()
		if weightPct > highestWeight {
			highestWeight = weightPct
		}
		if weightRatio.GreaterThan(threshold) {
			triggered = true
			if pos.StockID != 0 {
				stockMap[pos.StockID] = true
			}
			if pos.OrderbookID != "" {
				orderbookMap[pos.OrderbookID] = true
			}
		}
	}

	if !triggered {
		return res
	}

	res.Triggered = true
	res.Evidence.Metrics = map[string]any{
		"highest_weight_percent": highestWeight,
		"threshold_percent":      40.0,
	}

	for _, ord := range history.Orders {
		if (ord.StockID != 0 && stockMap[ord.StockID]) || (ord.OrderbookID != "" && orderbookMap[ord.OrderbookID]) {
			res.Evidence.OrderIDs = append(res.Evidence.OrderIDs, ord.ID)
		}
	}

	return res
}
