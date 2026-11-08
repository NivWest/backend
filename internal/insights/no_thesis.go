package insights

import (
	"context"
	"sort"
	"strings"

	"backend/internal/domain"
)

// NoThesisDetector triggers when more than 50% of the last 10 orders have no note.
type NoThesisDetector struct{}

func NewNoThesisDetector() domain.Detector {
	return &NoThesisDetector{}
}

func (d *NoThesisDetector) ID() domain.DetectorID {
	return domain.DetectorNoThesis
}

func (d *NoThesisDetector) Title() string {
	return "Missing trade thesis"
}

func (d *NoThesisDetector) Evaluate(_ context.Context, history domain.TradeHistory) domain.DetectorResult {
	res := domain.DetectorResult{
		ID:          d.ID(),
		Title:       d.Title(),
		Triggered:   false,
		Severity:    "info",
		Evidence:    domain.DetectorEvidence{OrderIDs: []uint{}},
		Explanation: "More than half of your recent trades lack a written thesis, making it difficult to review rationale.",
	}

	if len(history.Orders) == 0 {
		return res
	}

	// Sort orders newest first
	orders := make([]domain.OrderSummary, len(history.Orders))
	copy(orders, history.Orders)
	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return orders[i].ID > orders[j].ID
		}
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})

	limit := 10
	if len(orders) < limit {
		limit = len(orders)
	}
	recent := orders[:limit]

	var noNoteOrderIDs []uint
	for _, ord := range recent {
		if strings.TrimSpace(ord.Note) == "" {
			noNoteOrderIDs = append(noNoteOrderIDs, ord.ID)
		}
	}

	ratio := float64(len(noNoteOrderIDs)) / float64(len(recent))
	if ratio > 0.50 {
		res.Triggered = true
		res.Evidence.OrderIDs = noNoteOrderIDs
		res.Evidence.Metrics = map[string]any{
			"no_note_count":    len(noNoteOrderIDs),
			"evaluated_orders": len(recent),
			"ratio":            ratio,
			"threshold":        0.50,
		}
	}

	return res
}
