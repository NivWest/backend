package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/ai/prompts"
	"backend/internal/domain"
	"backend/internal/insights"

	"github.com/shopspring/decimal"
)

const (
	reviewMaxTokens int64 = 4000
	reviewEffort          = domain.LLMEffortLow
)

var reviewSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"headline", "observations", "habit_to_try"},
	"properties": map[string]any{
		"headline": map[string]any{"type": "string"},
		"observations": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"detector_id", "narrative", "order_ids"},
				"properties": map[string]any{
					"detector_id": map[string]any{"type": "string"},
					"narrative":   map[string]any{"type": "string"},
					"order_ids": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "integer"},
					},
				},
			},
		},
		"habit_to_try": map[string]any{"type": "string"},
	},
}

// InsightsService evaluates deterministic behavioral bias detectors and generates AI weekly reviews
// with server-side validated order citations (story NW-17).
type InsightsService struct {
	portfolio *PortfolioService
	orders    domain.OrderRepository
	reviews   domain.InsightsReviewRepository
	gateway   *AIGateway
	detectors []domain.Detector
	now       func() time.Time
}

func NewInsightsService(
	portfolio *PortfolioService,
	orders domain.OrderRepository,
	reviews domain.InsightsReviewRepository,
	gateway *AIGateway,
	detectors []domain.Detector,
	now func() time.Time,
) *InsightsService {
	if len(detectors) == 0 {
		detectors = insights.DefaultDetectors()
	}
	if now == nil {
		now = time.Now
	}
	return &InsightsService{
		portfolio: portfolio,
		orders:    orders,
		reviews:   reviews,
		gateway:   gateway,
		detectors: detectors,
		now:       now,
	}
}

// GetInsights evaluates all behavioral detectors for userID.
func (s *InsightsService) GetInsights(ctx context.Context, userID uint) (domain.Insights, error) {
	ins, _, err := s.evaluateWithOrders(ctx, userID)
	return ins, err
}

func (s *InsightsService) evaluateWithOrders(ctx context.Context, userID uint) (domain.Insights, []domain.Order, error) {
	pf, err := s.portfolio.GetPortfolio(ctx, userID)
	if err != nil {
		return domain.Insights{}, nil, fmt.Errorf("load portfolio for insights: %w", err)
	}
	orders, err := s.orders.ListAllByUser(ctx, userID)
	if err != nil {
		return domain.Insights{}, nil, fmt.Errorf("load orders for insights: %w", err)
	}

	positions := make([]domain.PositionSnapshot, len(pf.Positions))
	for i, p := range pf.Positions {
		positions[i] = domain.PositionSnapshot{
			OrderbookID:   p.OrderbookID,
			Symbol:        p.Symbol,
			Quantity:      decimal.NewFromFloat(p.Quantity),
			MarketValue:   decimal.NewFromFloat(p.MarketValue),
			AvgCost:       decimal.NewFromFloat(p.AvgCost),
			UnrealizedPnL: decimal.NewFromFloat(p.UnrealizedPnL),
		}
	}

	summaries := make([]domain.OrderSummary, len(orders))
	for i, o := range orders {
		summaries[i] = domain.OrderSummary{
			ID:          o.ID,
			OrderbookID: o.OrderbookID,
			Symbol:      o.Symbol,
			Side:        o.Side,
			Type:        o.Type,
			Status:      o.Status,
			Quantity:    o.Quantity,
			FillPrice:   o.FillPrice,
			Total:       o.Total,
			Note:        o.Note,
			RealizedPnL: o.RealizedPnL,
			CreatedAt:   o.CreatedAt,
		}
	}

	now := s.now().UTC()
	history := domain.TradeHistory{
		Now:         now,
		TotalEquity: decimal.NewFromFloat(pf.TotalEquity),
		Positions:   positions,
		Orders:      summaries,
	}

	results := make([]domain.DetectorResult, len(s.detectors))
	for i, det := range s.detectors {
		res := det.Evaluate(ctx, history)
		if res.Evidence.OrderIDs == nil {
			res.Evidence.OrderIDs = []uint{}
		}
		results[i] = res
	}

	return domain.Insights{
		GeneratedAt: now,
		Detectors:   results,
	}, orders, nil
}

type reviewDraft struct {
	Headline     string                     `json:"headline"`
	Observations []domain.ReviewObservation `json:"observations"`
	HabitToTry   string                     `json:"habit_to_try"`
}

// GenerateReview evaluates detectors, calls the LLM gateway, validates citations against the user's real orders
// and triggered detectors, and persists the review.
func (s *InsightsService) GenerateReview(ctx context.Context, userID uint) (domain.InsightsReview, error) {
	ins, orders, err := s.evaluateWithOrders(ctx, userID)
	if err != nil {
		return domain.InsightsReview{}, err
	}

	orderData := make([]prompts.InsightsOrderData, len(orders))
	for i, o := range orders {
		var pnlStr *string
		if o.RealizedPnL != nil {
			s := o.RealizedPnL.StringFixed(2)
			pnlStr = &s
		}
		orderData[i] = prompts.InsightsOrderData{
			ID:          o.ID,
			Side:        string(o.Side),
			Symbol:      o.Symbol,
			Quantity:    o.Quantity.Round(4).String(),
			FillPrice:   o.FillPrice.StringFixed(2),
			RealizedPnL: pnlStr,
			Note:        o.Note,
			CreatedAt:   o.CreatedAt.UTC().Format(time.RFC3339),
		}
	}

	prompt, err := prompts.InsightsReview(prompts.InsightsReviewData{
		Detectors: ins.Detectors,
		Orders:    orderData,
	})
	if err != nil {
		return domain.InsightsReview{}, err
	}

	resp, err := s.gateway.Generate(ctx, userID, domain.LLMRequest{
		Feature:       domain.AIFeatureInsightsReview,
		PromptVersion: prompt.Version,
		System:        prompt.System,
		Messages:      []domain.LLMMessage{{Role: domain.LLMRoleUser, Text: prompt.User}},
		JSONSchema:    reviewSchema,
		MaxTokens:     reviewMaxTokens,
		Effort:        reviewEffort,
	})
	if err != nil {
		return domain.InsightsReview{}, err
	}

	draft, err := parseReviewDraft(resp.Content)
	if err != nil {
		return domain.InsightsReview{}, fmt.Errorf("%w: %w", domain.ErrAIUnavailable, err)
	}

	validatedObs := validateReviewObservations(draft.Observations, ins.Detectors, orders)

	review := domain.InsightsReview{
		GeneratedAt:  s.now().UTC(),
		Headline:     strings.TrimSpace(draft.Headline),
		Observations: validatedObs,
		HabitToTry:   strings.TrimSpace(draft.HabitToTry),
		Disclaimer:   domain.AIDisclaimer,
	}

	if err := s.reviews.Create(ctx, &review, userID, resp.Model, prompt.Version); err != nil {
		return domain.InsightsReview{}, fmt.Errorf("persist insights review: %w", err)
	}
	return review, nil
}

// ListReviews returns historical reviews for userID, newest first.
func (s *InsightsService) ListReviews(ctx context.Context, userID uint) (domain.InsightsReviewListResponse, error) {
	items, err := s.reviews.List(ctx, userID, 20)
	if err != nil {
		return domain.InsightsReviewListResponse{}, err
	}
	if items == nil {
		items = []domain.InsightsReview{}
	}
	return domain.InsightsReviewListResponse{
		Items:      items,
		NextCursor: nil,
	}, nil
}

func parseReviewDraft(content string) (reviewDraft, error) {
	var d reviewDraft
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return reviewDraft{}, fmt.Errorf("invalid review JSON: %w", err)
	}
	if strings.TrimSpace(d.Headline) == "" || strings.TrimSpace(d.HabitToTry) == "" {
		return reviewDraft{}, errors.New("invalid review: headline and habit_to_try are required")
	}
	return d, nil
}

// validateReviewObservations drops observations whose detector_id is not a triggered detector
// and strips any order_ids that do not belong to the user's actual orders.
func validateReviewObservations(
	raw []domain.ReviewObservation,
	detectors []domain.DetectorResult,
	orders []domain.Order,
) []domain.ReviewObservation {
	triggered := make(map[string]bool, len(detectors))
	for _, d := range detectors {
		if d.Triggered {
			triggered[string(d.ID)] = true
		}
	}

	ownedOrders := make(map[uint]bool, len(orders))
	for _, o := range orders {
		ownedOrders[o.ID] = true
	}

	out := make([]domain.ReviewObservation, 0, len(raw))
	for _, obs := range raw {
		if !triggered[obs.DetectorID] {
			continue
		}
		validIDs := make([]uint, 0, len(obs.OrderIDs))
		seen := make(map[uint]bool, len(obs.OrderIDs))
		for _, id := range obs.OrderIDs {
			if ownedOrders[id] && !seen[id] {
				seen[id] = true
				validIDs = append(validIDs, id)
			}
		}
		out = append(out, domain.ReviewObservation{
			DetectorID: obs.DetectorID,
			Narrative:  strings.TrimSpace(obs.Narrative),
			OrderIDs:   validIDs,
		})
	}
	return out
}
