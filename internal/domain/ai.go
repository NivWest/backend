package domain

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// AI features (story NW-14). Shapes: api/openapi.yaml#/components/schemas/{InstrumentBrief,AIUsage}.

// AIFeature names a feature that calls the LLM gateway (#/components/schemas/AIFeature).
type AIFeature string

const (
	AIFeatureInstrumentBrief AIFeature = "instrument_brief"
)

// AIFeatures lists every feature, sorted by name; GET /ai/usage reports each one.
var AIFeatures = []AIFeature{AIFeatureInsightsReview, AIFeatureInstrumentBrief}

// AIDisclaimer labels every piece of AI output (docs/conventions.md).
const AIDisclaimer = "AI-generated · educational, not financial advice"

// DefaultAIDailyTokenBudget is the per-user daily token budget when AI_DAILY_TOKEN_BUDGET is unset.
const DefaultAIDailyTokenBudget int64 = 200000

// AI error codes, returned as ErrorResponse.Code.
const (
	CodeAIBudgetExceeded = "AI_BUDGET_EXCEEDED"
	CodeAIRefused        = "AI_REFUSED"
	CodeAIUnavailable    = "AI_UNAVAILABLE"
)

var (
	// ErrAIBudgetExceeded means the user's daily token budget is used up. Handlers map it to 429 AI_BUDGET_EXCEEDED.
	ErrAIBudgetExceeded = errors.New("daily AI token budget exceeded, try again tomorrow")
	// ErrAIRefused means the model declined (stop_reason refusal), also after any fallback. 422 AI_REFUSED.
	ErrAIRefused = errors.New("the AI declined to answer this request")
	// ErrAIUnavailable means the provider failed, timed out, hit max_tokens or returned an invalid answer.
	// Adapters and services wrap it with %w so the cause stays in the chain. 503 AI_UNAVAILABLE.
	ErrAIUnavailable = errors.New("the AI service is unavailable, please try again later")
)

// AI usage ledger statuses.
const (
	AIUsageStatusOK      = "ok"
	AIUsageStatusError   = "error"
	AIUsageStatusRefused = "refused"
)

// AIUsageRecord is one ledger row: one model call by one user.
type AIUsageRecord struct {
	UserID        uint
	Feature       AIFeature
	Model         string
	PromptVersion string
	Usage         LLMUsage
	LatencyMs     int64
	Status        string
	// FallbackModel is the model that answered after a server-side refusal fallback ("" when none ran).
	FallbackModel string
	// CostUSD is the estimated cost from the per-model price table (zero for unknown models).
	CostUSD   decimal.Decimal
	CreatedAt time.Time
}

// AIFeatureUsage is one feature's totals for a user and day (#/components/schemas/AIFeatureUsage).
// The cache token totals count towards the budget but are not part of the API body.
type AIFeatureUsage struct {
	Feature          AIFeature `json:"feature"`
	Calls            int64     `json:"calls"`
	InputTokens      int64     `json:"input_tokens"`
	OutputTokens     int64     `json:"output_tokens"`
	CacheReadTokens  int64     `json:"-"`
	CacheWriteTokens int64     `json:"-"`
}

// TotalTokens is every token the feature's calls processed.
func (u AIFeatureUsage) TotalTokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// AIUsage is the body of GET /ai/usage (#/components/schemas/AIUsage).
type AIUsage struct {
	Date            string           `json:"date"`
	BudgetTokens    int64            `json:"budget_tokens"`
	UsedTokens      int64            `json:"used_tokens"`
	RemainingTokens int64            `json:"remaining_tokens"`
	ByFeature       []AIFeatureUsage `json:"by_feature"`
}

// AIUsageRepository is the usage ledger.
type AIUsageRepository interface {
	Record(ctx context.Context, rec AIUsageRecord) error
	// DailyByFeature sums the user's rows created in [from, to) per feature. Features without rows are absent.
	DailyByFeature(ctx context.Context, userID uint, from, to time.Time) ([]AIFeatureUsage, error)
}

// BriefMetric is one explained number (#/components/schemas/BriefMetric).
type BriefMetric struct {
	Metric      string `json:"metric"`
	Value       string `json:"value"`
	Explanation string `json:"explanation"`
}

// Brief is the model's structured answer (#/components/schemas/Brief).
type Brief struct {
	Summary           string        `json:"summary"`
	WhatTheNumbersSay []BriefMetric `json:"what_the_numbers_say"`
	Risks             []string      `json:"risks"`
	BeginnerTakeaway  string        `json:"beginner_takeaway"`
}

// InstrumentBrief is the body of GET /ai/instruments/{orderbook_id}/brief (#/components/schemas/InstrumentBrief).
type InstrumentBrief struct {
	OrderbookID   string    `json:"orderbook_id"`
	GeneratedAt   time.Time `json:"generated_at"`
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	Cached        bool      `json:"cached"`
	Disclaimer    string    `json:"disclaimer"`
	Brief         Brief     `json:"brief"`
}

// ErrBriefNotFound means no brief is cached for the key.
var ErrBriefNotFound = errors.New("instrument brief not cached")

// StoredBrief is one cached brief, keyed by (OrderbookID, UTCDate, PromptVersion).
type StoredBrief struct {
	OrderbookID   string
	UTCDate       string // YYYY-MM-DD
	PromptVersion string
	Model         string
	Brief         Brief
	GeneratedAt   time.Time
}

// AIBriefRepository is the shared brief cache.
type AIBriefRepository interface {
	// Get returns the cached brief or ErrBriefNotFound.
	Get(ctx context.Context, orderbookID, utcDate, promptVersion string) (StoredBrief, error)
	// Save stores b unless a brief with the same key exists, and returns the stored one (the first writer wins).
	Save(ctx context.Context, b StoredBrief) (StoredBrief, error)
}
