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

	"golang.org/x/sync/singleflight"
)

// Instrument brief settings (story NW-14): a short structured answer, so low effort and a small token cap.
const (
	briefMaxTokens int64 = 4000
	briefEffort          = domain.LLMEffortLow
)

// briefSchema is the structured-output schema of instrument_brief (#/components/schemas/Brief).
var briefSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"summary", "what_the_numbers_say", "risks", "beginner_takeaway"},
	"properties": map[string]any{
		"summary": map[string]any{"type": "string"},
		"what_the_numbers_say": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"metric", "value", "explanation"},
				"properties": map[string]any{
					"metric":      map[string]any{"type": "string"},
					"value":       map[string]any{"type": "string"},
					"explanation": map[string]any{"type": "string"},
				},
			},
		},
		"risks":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"beginner_takeaway": map[string]any{"type": "string"},
	},
}

// AIBriefService serves GET /ai/instruments/{id}/brief. Briefs are cached per (instrument, UTC day, prompt
// version) and shared by every user; misses go through the gateway (budget + ledger), and concurrent misses
// for one key share one model call (singleflight). The instrument is looked up first, so an unknown one fails
// with ErrInstrumentNotFound before any budget check or model call.
type AIBriefService struct {
	gateway *AIGateway
	market  domain.MarketDataProvider
	briefs  domain.AIBriefRepository
	now     func() time.Time
	flight  singleflight.Group
	// generateTimeout bounds a shared generation that outlives the request that started it.
	generateTimeout time.Duration
}

func NewAIBriefService(gateway *AIGateway, market domain.MarketDataProvider, briefs domain.AIBriefRepository, now func() time.Time) *AIBriefService {
	if now == nil {
		now = time.Now
	}
	return &AIBriefService{gateway: gateway, market: market, briefs: briefs, now: now, generateTimeout: gateway.timeout + 5*time.Second}
}

// Brief returns today's brief of the instrument for userID, generating it on a cache miss.
func (s *AIBriefService) Brief(ctx context.Context, userID uint, orderbookID string) (domain.InstrumentBrief, error) {
	id, err := validateOrderbookID(orderbookID)
	if err != nil {
		return domain.InstrumentBrief{}, err
	}
	details, err := s.market.Details(ctx, id)
	if err != nil {
		return domain.InstrumentBrief{}, err
	}
	details.OrderbookID = id // the cache key is the requested id
	_, _, day := utcDay(s.now())
	version := prompts.InstrumentBriefVersion

	if stored, err := s.briefs.Get(ctx, id, day, version); err == nil {
		return toInstrumentBrief(stored, true), nil
	} else if !errors.Is(err, domain.ErrBriefNotFound) {
		return domain.InstrumentBrief{}, fmt.Errorf("read brief cache: %w", err)
	}

	// Each caller's own budget gates a miss; only the caller whose closure runs makes (and pays for) the call.
	if err := s.gateway.CheckBudget(ctx, userID); err != nil {
		return domain.InstrumentBrief{}, err
	}
	generated := false
	key := id + "|" + day + "|" + version
	v, err, _ := s.flight.Do(key, func() (any, error) {
		// The shared call must not die with the first caller's request, so detach it and bound it instead.
		genCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.generateTimeout)
		defer cancel()
		if stored, err := s.briefs.Get(genCtx, id, day, version); err == nil {
			return stored, nil // filled by a flight that finished just before this one started
		}
		stored, err := s.generate(genCtx, userID, details, day)
		if err == nil {
			generated = true
		}
		return stored, err
	})
	if err != nil {
		return domain.InstrumentBrief{}, err
	}
	return toInstrumentBrief(v.(domain.StoredBrief), !generated), nil
}

func (s *AIBriefService) generate(ctx context.Context, userID uint, d domain.InstrumentDetails, day string) (domain.StoredBrief, error) {
	prompt, err := prompts.InstrumentBrief(prompts.InstrumentData{
		OrderbookID:      d.OrderbookID,
		Symbol:           d.Symbol,
		Name:             d.Name,
		Sector:           d.Sector,
		Market:           d.Market,
		Currency:         d.Currency,
		Description:      d.Description,
		LastPrice:        d.LastPrice,
		ChangePercent:    d.ChangePercent,
		PERatio:          d.KeyIndicators.PERatio,
		DividendYieldPct: d.KeyIndicators.DividendYieldPct,
		MarketCap:        d.KeyIndicators.MarketCap,
	})
	if err != nil {
		return domain.StoredBrief{}, err
	}
	resp, err := s.gateway.Generate(ctx, userID, domain.LLMRequest{
		Feature:       domain.AIFeatureInstrumentBrief,
		PromptVersion: prompt.Version,
		System:        prompt.System,
		Messages:      []domain.LLMMessage{{Role: domain.LLMRoleUser, Text: prompt.User}},
		JSONSchema:    briefSchema,
		MaxTokens:     briefMaxTokens,
		Effort:        briefEffort,
	})
	if err != nil {
		return domain.StoredBrief{}, err
	}
	brief, err := parseBrief(resp.Content)
	if err != nil {
		return domain.StoredBrief{}, fmt.Errorf("%w: %w", domain.ErrAIUnavailable, err)
	}
	return s.briefs.Save(ctx, domain.StoredBrief{
		OrderbookID:   d.OrderbookID,
		UTCDate:       day,
		PromptVersion: prompt.Version,
		Model:         resp.Model,
		Brief:         brief,
		GeneratedAt:   s.now().UTC(),
	})
}

// parseBrief decodes and checks the model's JSON: summary and takeaway are required, arrays never nil.
func parseBrief(content string) (domain.Brief, error) {
	var b domain.Brief
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return domain.Brief{}, fmt.Errorf("invalid brief JSON: %w", err)
	}
	if strings.TrimSpace(b.Summary) == "" || strings.TrimSpace(b.BeginnerTakeaway) == "" {
		return domain.Brief{}, errors.New("invalid brief: summary and beginner_takeaway are required")
	}
	if b.WhatTheNumbersSay == nil {
		b.WhatTheNumbersSay = []domain.BriefMetric{}
	}
	if b.Risks == nil {
		b.Risks = []string{}
	}
	return b, nil
}

func toInstrumentBrief(s domain.StoredBrief, cached bool) domain.InstrumentBrief {
	return domain.InstrumentBrief{
		OrderbookID:   s.OrderbookID,
		GeneratedAt:   s.GeneratedAt.UTC(),
		Model:         s.Model,
		PromptVersion: s.PromptVersion,
		Cached:        cached,
		Disclaimer:    domain.AIDisclaimer,
		Brief:         s.Brief,
	}
}
