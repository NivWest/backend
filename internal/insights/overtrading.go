package insights

import (
	"context"
	"time"

	"backend/internal/domain"
)

// OvertradingDetector triggers when a user averages more than 10 orders per day over the last 7 days (counting only days with orders).
type OvertradingDetector struct{}

func NewOvertradingDetector() domain.Detector {
	return &OvertradingDetector{}
}

func (d *OvertradingDetector) ID() domain.DetectorID {
	return domain.DetectorOvertrading
}

func (d *OvertradingDetector) Title() string {
	return "Overtrading"
}

func (d *OvertradingDetector) Evaluate(_ context.Context, history domain.TradeHistory) domain.DetectorResult {
	res := domain.DetectorResult{
		ID:          d.ID(),
		Title:       d.Title(),
		Triggered:   false,
		Severity:    "warning",
		Evidence:    domain.DetectorEvidence{OrderIDs: []uint{}},
		Explanation: "Averaging more than 10 trades per active day can lead to high commission drag and reduced focus.",
	}

	if len(history.Orders) == 0 {
		return res
	}

	evalTime := history.Now
	if evalTime.IsZero() {
		evalTime = time.Now().UTC()
	}

	cutoff := evalTime.Add(-7 * 24 * time.Hour)
	ordersByDay := make(map[string][]uint)
	var recentOrderIDs []uint

	for _, ord := range history.Orders {
		if ord.CreatedAt.Before(cutoff) {
			continue
		}
		dayKey := ord.CreatedAt.UTC().Format("2006-01-02")
		ordersByDay[dayKey] = append(ordersByDay[dayKey], ord.ID)
		recentOrderIDs = append(recentOrderIDs, ord.ID)
	}

	daysCount := len(ordersByDay)
	if daysCount == 0 {
		return res
	}

	totalOrders := len(recentOrderIDs)
	avg := float64(totalOrders) / float64(daysCount)

	if avg > 10.0 {
		res.Triggered = true
		res.Evidence.OrderIDs = recentOrderIDs
		res.Evidence.Metrics = map[string]any{
			"average_orders_per_day": avg,
			"active_days":            daysCount,
			"total_orders":           totalOrders,
			"threshold":              10.0,
		}
	}

	return res
}
