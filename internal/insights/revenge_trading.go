package insights

import (
	"context"
	"sort"
	"time"

	"backend/internal/domain"
)

// RevengeTradingDetector triggers when a user places a BUY within 10 minutes after a SELL that realized a loss.
type RevengeTradingDetector struct{}

func NewRevengeTradingDetector() domain.Detector {
	return &RevengeTradingDetector{}
}

func (d *RevengeTradingDetector) ID() domain.DetectorID {
	return domain.DetectorRevengeTrading
}

func (d *RevengeTradingDetector) Title() string {
	return "Revenge trading"
}

func (d *RevengeTradingDetector) Evaluate(_ context.Context, history domain.TradeHistory) domain.DetectorResult {
	res := domain.DetectorResult{
		ID:          d.ID(),
		Title:       d.Title(),
		Triggered:   false,
		Severity:    "warning",
		Evidence:    domain.DetectorEvidence{OrderIDs: []uint{}},
		Explanation: "Placing a buy order within 10 minutes of realizing a loss often indicates emotional decision-making.",
	}

	if len(history.Orders) < 2 {
		return res
	}

	// Sort orders chronologically ascending
	orders := make([]domain.OrderSummary, len(history.Orders))
	copy(orders, history.Orders)
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].ID < orders[j].ID
		}
		return orders[i].CreatedAt.Before(orders[j].CreatedAt)
	})

	const window = 10 * time.Minute
	orderIDSet := make(map[uint]bool)
	var orderIDs []uint

	for i := 0; i < len(orders); i++ {
		sell := orders[i]
		if sell.Side != domain.OrderSideSell || sell.RealizedPnL == nil || !sell.RealizedPnL.IsNegative() {
			continue
		}

		// Look for a subsequent BUY within 10 minutes
		for j := i + 1; j < len(orders); j++ {
			buy := orders[j]
			diff := buy.CreatedAt.Sub(sell.CreatedAt)
			if diff < 0 {
				continue
			}
			if diff > window {
				break
			}
			if buy.Side == domain.OrderSideBuy {
				if !orderIDSet[sell.ID] {
					orderIDSet[sell.ID] = true
					orderIDs = append(orderIDs, sell.ID)
				}
				if !orderIDSet[buy.ID] {
					orderIDSet[buy.ID] = true
					orderIDs = append(orderIDs, buy.ID)
				}
			}
		}
	}

	if len(orderIDs) > 0 {
		res.Triggered = true
		res.Evidence.OrderIDs = orderIDs
		res.Evidence.Metrics = map[string]any{
			"window_minutes": 10,
			"events_count":   len(orderIDs) / 2,
		}
	}

	return res
}
