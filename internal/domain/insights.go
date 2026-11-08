package domain

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Behavioral insights and pattern detection (story NW-17).
// Shapes: api/openapi.yaml#/components/schemas/{Insights,DetectorResult,InsightsReview}.

// DetectorID identifies a behavioral bias detector (#/components/schemas/DetectorID).
type DetectorID string

const (
	DetectorConcentration  DetectorID = "concentration"
	DetectorRevengeTrading DetectorID = "revenge_trading"
	DetectorOvertrading    DetectorID = "overtrading"
	DetectorDisposition    DetectorID = "disposition_effect"
	DetectorNoThesis       DetectorID = "no_thesis"
)

// AllDetectorIDs lists all supported detector IDs.
var AllDetectorIDs = []DetectorID{
	DetectorConcentration,
	DetectorRevengeTrading,
	DetectorOvertrading,
	DetectorDisposition,
	DetectorNoThesis,
}

// AIFeatureInsightsReview is the AI feature name for behavioral weekly reviews.
const AIFeatureInsightsReview AIFeature = "insights_review"

// DetectorEvidence is the evidence supporting a triggered detector (#/components/schemas/DetectorEvidence).
type DetectorEvidence struct {
	OrderIDs []uint         `json:"order_ids"`
	Metrics  map[string]any `json:"metrics,omitempty"`
}

// DetectorResult is the outcome of one detector evaluation (#/components/schemas/DetectorResult).
type DetectorResult struct {
	ID          DetectorID       `json:"id"`
	Title       string           `json:"title"`
	Triggered   bool             `json:"triggered"`
	Severity    string           `json:"severity"` // "info" | "warning" | "critical"
	Evidence    DetectorEvidence `json:"evidence"`
	Explanation string           `json:"explanation"`
}

// Insights is the collection of evaluated detectors for a user (#/components/schemas/Insights).
type Insights struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Detectors   []DetectorResult `json:"detectors"`
}

// PositionSnapshot is a snapshot of an open position for detector evaluation.
type PositionSnapshot struct {
	StockID       uint
	OrderbookID   string
	Symbol        string
	Quantity      decimal.Decimal
	MarketValue   decimal.Decimal
	AvgCost       decimal.Decimal
	UnrealizedPnL decimal.Decimal
}

// OrderSummary is a compact representation of an order for detector evaluation and AI review.
type OrderSummary struct {
	ID          uint
	StockID     uint
	OrderbookID string
	Symbol      string
	Side        OrderSide
	Type        OrderType
	Status      OrderStatus
	Quantity    decimal.Decimal
	FillPrice   decimal.Decimal
	Total       decimal.Decimal
	Note        string
	RealizedPnL *decimal.Decimal
	CreatedAt   time.Time
}

// TradeHistory encapsulates all user trading history and holdings needed by detectors.
// No I/O inside detectors: history is loaded once by the service.
type TradeHistory struct {
	Now         time.Time
	TotalEquity decimal.Decimal
	Positions   []PositionSnapshot
	Orders      []OrderSummary
}

// Detector is the interface implemented by each behavioral bias detector.
type Detector interface {
	ID() DetectorID
	Title() string
	Evaluate(ctx context.Context, history TradeHistory) DetectorResult
}

// ReviewObservation is one cited observation in an AI insights review (#/components/schemas/ReviewObservation).
type ReviewObservation struct {
	DetectorID string `json:"detector_id"`
	Narrative  string `json:"narrative"`
	OrderIDs   []uint `json:"order_ids"`
}

// InsightsReview is an AI weekly behavioral review (#/components/schemas/InsightsReview).
type InsightsReview struct {
	ID           uint                `json:"id"`
	GeneratedAt  time.Time           `json:"generated_at"`
	Headline     string              `json:"headline"`
	Observations []ReviewObservation `json:"observations"`
	HabitToTry   string              `json:"habit_to_try"`
	Disclaimer   string              `json:"disclaimer"`
}

// InsightsReviewRepository persists generated weekly reviews.
type InsightsReviewRepository interface {
	Create(ctx context.Context, review *InsightsReview, userID uint, model, promptVersion string) error
	List(ctx context.Context, userID uint, limit int) ([]InsightsReview, error)
}
