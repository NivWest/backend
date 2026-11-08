package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"backend/internal/config"
	"backend/internal/domain"

	"github.com/shopspring/decimal"
)

// AIGateway is the single entry point for model calls (story NW-14). It wraps any domain.LLM with:
//   - a per-user daily token budget (UTC day), checked before each call: ErrAIBudgetExceeded;
//   - a timeout per call (LLM_TIMEOUT), applied through the context;
//   - one ledger row per call (feature, model, prompt version, tokens, latency, status, fallback, cost);
//   - mapping of anything the provider returns that is not an AI error to ErrAIUnavailable.
//
// The budget check and the record are not one transaction: concurrent calls of one user may each pass the
// check and overshoot the budget by at most one call each. The next call is refused.
type AIGateway struct {
	llm     domain.LLM
	usage   domain.AIUsageRepository
	budget  int64
	timeout time.Duration
	prices  map[string]config.ModelPrice
	now     func() time.Time
	metrics domain.AIMetricsObserver
}

// NewAIGateway builds the gateway. budget <= 0 uses domain.DefaultAIDailyTokenBudget, timeout <= 0 means 60s.
func NewAIGateway(
	llm domain.LLM,
	usage domain.AIUsageRepository,
	budget int64,
	timeout time.Duration,
	prices map[string]config.ModelPrice,
	now func() time.Time,
) *AIGateway {
	if budget <= 0 {
		budget = domain.DefaultAIDailyTokenBudget
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if now == nil {
		now = time.Now
	}
	return &AIGateway{llm: llm, usage: usage, budget: budget, timeout: timeout, prices: prices, now: now}
}

// Provider is the wrapped provider's name ("anthropic" or "fake").
func (g *AIGateway) Provider() string { return g.llm.Name() }

// SetMetricsObserver sets the observer for Prometheus metrics.
func (g *AIGateway) SetMetricsObserver(m domain.AIMetricsObserver) {
	g.metrics = m
}

// utcDay returns the start and end of the UTC day containing t, and its YYYY-MM-DD date.
func utcDay(t time.Time) (from, to time.Time, date string) {
	t = t.UTC()
	from = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 0, 1), from.Format(time.DateOnly)
}

// Usage returns the user's usage for the current UTC day, with every feature of domain.AIFeatures listed.
func (g *AIGateway) Usage(ctx context.Context, userID uint) (domain.AIUsage, error) {
	from, to, date := utcDay(g.now())
	rows, err := g.usage.DailyByFeature(ctx, userID, from, to)
	if err != nil {
		return domain.AIUsage{}, fmt.Errorf("read ai usage: %w", err)
	}
	byFeature := map[domain.AIFeature]domain.AIFeatureUsage{}
	var used int64
	for _, r := range rows {
		byFeature[r.Feature] = r
		used += r.TotalTokens()
	}
	out := domain.AIUsage{
		Date:            date,
		BudgetTokens:    g.budget,
		UsedTokens:      used,
		RemainingTokens: max(0, g.budget-used),
		ByFeature:       make([]domain.AIFeatureUsage, 0, len(domain.AIFeatures)),
	}
	for _, f := range domain.AIFeatures {
		u, ok := byFeature[f]
		if !ok {
			u = domain.AIFeatureUsage{Feature: f}
		}
		out.ByFeature = append(out.ByFeature, u)
	}
	return out, nil
}

// CheckBudget returns ErrAIBudgetExceeded when the user has no tokens left today.
func (g *AIGateway) CheckBudget(ctx context.Context, userID uint) error {
	u, err := g.Usage(ctx, userID)
	if err != nil {
		return err
	}
	if u.RemainingTokens <= 0 {
		return domain.ErrAIBudgetExceeded
	}
	return nil
}

// Generate checks the budget, calls the model and records the call.
func (g *AIGateway) Generate(ctx context.Context, userID uint, req domain.LLMRequest) (domain.LLMResponse, error) {
	return g.call(ctx, userID, req, func(ctx context.Context) (domain.LLMResponse, error) {
		return g.llm.Generate(ctx, req)
	})
}

// Stream is Generate with streamed events (used by the chat, NW-16).
func (g *AIGateway) Stream(ctx context.Context, userID uint, req domain.LLMRequest, onEvent func(domain.LLMEvent) error) (domain.LLMResponse, error) {
	return g.call(ctx, userID, req, func(ctx context.Context) (domain.LLMResponse, error) {
		return g.llm.Stream(ctx, req, onEvent)
	})
}

func (g *AIGateway) call(
	ctx context.Context,
	userID uint,
	req domain.LLMRequest,
	run func(context.Context) (domain.LLMResponse, error),
) (domain.LLMResponse, error) {
	if err := g.CheckBudget(ctx, userID); err != nil {
		return domain.LLMResponse{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	start := g.now()
	resp, err := run(callCtx)
	latency := resp.Latency
	if latency <= 0 {
		latency = g.now().Sub(start)
	}
	resp.Latency = latency

	status := domain.AIUsageStatusOK
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrAIRefused):
		status = domain.AIUsageStatusRefused
	case errors.Is(err, domain.ErrAIUnavailable):
		status = domain.AIUsageStatusError
	default:
		// Context deadline, cancellation or an unmapped provider error.
		status = domain.AIUsageStatusError
		err = fmt.Errorf("%w: %w", domain.ErrAIUnavailable, err)
	}

	model := resp.Model
	if model == "" {
		model = "unknown"
	}
	rec := domain.AIUsageRecord{
		UserID:        userID,
		Feature:       req.Feature,
		Model:         model,
		PromptVersion: req.PromptVersion,
		Usage:         resp.Usage,
		LatencyMs:     latency.Milliseconds(),
		Status:        status,
		FallbackModel: resp.FallbackModel,
		CostUSD:       g.cost(model, resp.Usage),
		CreatedAt:     g.now().UTC(),
	}
	// Record even when the caller went away: the tokens were spent.
	if recErr := g.usage.Record(context.WithoutCancel(ctx), rec); recErr != nil {
		slog.Error("failed to record AI usage", "user_id", userID, "feature", req.Feature, "error", recErr)
	}
	if resp.FallbackModel != "" {
		slog.Info("refusal fallback served", "user_id", userID, "feature", req.Feature, "fallback_model", resp.FallbackModel)
	}
	if g.metrics != nil {
		g.metrics.ObserveLLMCall(
			string(req.Feature),
			g.llm.Name(),
			string(status),
			latency,
			resp.Usage.Input,
			resp.Usage.Output,
			rec.CostUSD.InexactFloat64(),
		)
	}
	return resp, err
}

var perMillion = decimal.NewFromInt(1_000_000)

// cost is the estimated USD cost of usage on model, from the price table; unknown models cost 0.
func (g *AIGateway) cost(model string, u domain.LLMUsage) decimal.Decimal {
	p, ok := g.prices[model]
	if !ok {
		return decimal.Zero
	}
	part := func(tokens int64, perMTok float64) decimal.Decimal {
		return decimal.NewFromInt(tokens).Mul(decimal.NewFromFloat(perMTok))
	}
	total := part(u.Input, p.InputPerMTok).
		Add(part(u.Output, p.OutputPerMTok)).
		Add(part(u.CacheRead, p.CacheReadPerMTok)).
		Add(part(u.CacheWrite, p.CacheWritePerMTok))
	return total.Div(perMillion).Round(6)
}
