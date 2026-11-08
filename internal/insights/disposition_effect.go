package insights

import (
	"context"

	"backend/internal/domain"
)

// DispositionEffectDetector triggers when the user has at least 4 sells and Odean's PGR - PLR > 0.20.
// PGR (Proportion of Gains Realized) = Realized Gains / (Realized Gains + Paper Gains)
// PLR (Proportion of Losses Realized) = Realized Losses / (Realized Losses + Paper Losses)
type DispositionEffectDetector struct{}

func NewDispositionEffectDetector() domain.Detector {
	return &DispositionEffectDetector{}
}

func (d *DispositionEffectDetector) ID() domain.DetectorID {
	return domain.DetectorDisposition
}

func (d *DispositionEffectDetector) Title() string {
	return "Disposition effect"
}

func (d *DispositionEffectDetector) Evaluate(_ context.Context, history domain.TradeHistory) domain.DetectorResult {
	res := domain.DetectorResult{
		ID:          d.ID(),
		Title:       d.Title(),
		Triggered:   false,
		Severity:    "warning",
		Evidence:    domain.DetectorEvidence{OrderIDs: []uint{}},
		Explanation: "Realizing gains too quickly while holding losing positions (PGR - PLR > 0.20) can limit upside.",
	}

	var sells []domain.OrderSummary
	var sellOrderIDs []uint
	realizedGainsCount := 0
	realizedLossesCount := 0

	for _, ord := range history.Orders {
		if ord.Side != domain.OrderSideSell {
			continue
		}
		sells = append(sells, ord)
		sellOrderIDs = append(sellOrderIDs, ord.ID)
		if ord.RealizedPnL != nil {
			if ord.RealizedPnL.IsPositive() {
				realizedGainsCount++
			} else if ord.RealizedPnL.IsNegative() {
				realizedLossesCount++
			}
		}
	}

	// Criterion: at least 4 sells
	if len(sells) < 4 {
		return res
	}

	paperGainsCount := 0
	paperLossesCount := 0
	for _, pos := range history.Positions {
		if pos.UnrealizedPnL.IsPositive() {
			paperGainsCount++
		} else if pos.UnrealizedPnL.IsNegative() {
			paperLossesCount++
		}
	}

	pgr := 0.0
	if denom := realizedGainsCount + paperGainsCount; denom > 0 {
		pgr = float64(realizedGainsCount) / float64(denom)
	}

	plr := 0.0
	if denom := realizedLossesCount + paperLossesCount; denom > 0 {
		plr = float64(realizedLossesCount) / float64(denom)
	}

	diff := pgr - plr

	if diff > 0.20 {
		res.Triggered = true
		res.Evidence.OrderIDs = sellOrderIDs
		res.Evidence.Metrics = map[string]any{
			"pgr":                   pgr,
			"plr":                   plr,
			"diff":                  diff,
			"threshold":             0.20,
			"sells_count":           len(sells),
			"realized_gains_count":  realizedGainsCount,
			"realized_losses_count": realizedLossesCount,
			"paper_gains_count":     paperGainsCount,
			"paper_losses_count":    paperLossesCount,
		}
	}

	return res
}
