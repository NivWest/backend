package testutil

import (
	"context"
	"sort"
	"sync"
	"time"

	"backend/internal/domain"
)

var (
	_ domain.AIUsageRepository        = (*AIUsageRepo)(nil)
	_ domain.AIBriefRepository        = (*AIBriefRepo)(nil)
	_ domain.InsightsReviewRepository = (*InsightsReviewRepo)(nil)
	_ domain.LLM                      = (*LLM)(nil)
)

// ---------------------------------------------------------------------------
// AIUsageRepository
// ---------------------------------------------------------------------------

// AIUsageRepo is an in-memory domain.AIUsageRepository. RecordErr, when set, fails every Record.
type AIUsageRepo struct {
	mu        sync.Mutex
	Records   []domain.AIUsageRecord
	RecordErr error
	DailyErr  error
}

func NewAIUsageRepo() *AIUsageRepo { return &AIUsageRepo{} }

func (r *AIUsageRepo) Record(_ context.Context, rec domain.AIUsageRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.RecordErr != nil {
		return r.RecordErr
	}
	r.Records = append(r.Records, rec)
	return nil
}

func (r *AIUsageRepo) DailyByFeature(_ context.Context, userID uint, from, to time.Time) ([]domain.AIFeatureUsage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.DailyErr != nil {
		return nil, r.DailyErr
	}
	sums := map[domain.AIFeature]*domain.AIFeatureUsage{}
	for _, rec := range r.Records {
		if rec.UserID != userID || rec.CreatedAt.Before(from) || !rec.CreatedAt.Before(to) {
			continue
		}
		s, ok := sums[rec.Feature]
		if !ok {
			s = &domain.AIFeatureUsage{Feature: rec.Feature}
			sums[rec.Feature] = s
		}
		s.Calls++
		s.InputTokens += rec.Usage.Input
		s.OutputTokens += rec.Usage.Output
		s.CacheReadTokens += rec.Usage.CacheRead
		s.CacheWriteTokens += rec.Usage.CacheWrite
	}
	out := make([]domain.AIFeatureUsage, 0, len(sums))
	for _, s := range sums {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Feature < out[j].Feature })
	return out, nil
}

// Snapshot returns a copy of the recorded rows.
func (r *AIUsageRepo) Snapshot() []domain.AIUsageRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.AIUsageRecord(nil), r.Records...)
}

// ---------------------------------------------------------------------------
// AIBriefRepository
// ---------------------------------------------------------------------------

// AIBriefRepo is an in-memory domain.AIBriefRepository with first-writer-wins Save.
type AIBriefRepo struct {
	mu      sync.Mutex
	briefs  map[string]domain.StoredBrief
	Saves   int
	GetErr  error
	SaveErr error
}

func NewAIBriefRepo() *AIBriefRepo { return &AIBriefRepo{briefs: map[string]domain.StoredBrief{}} }

func briefKey(id, day, version string) string { return id + "|" + day + "|" + version }

func (r *AIBriefRepo) Get(_ context.Context, orderbookID, utcDate, promptVersion string) (domain.StoredBrief, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.GetErr != nil {
		return domain.StoredBrief{}, r.GetErr
	}
	b, ok := r.briefs[briefKey(orderbookID, utcDate, promptVersion)]
	if !ok {
		return domain.StoredBrief{}, domain.ErrBriefNotFound
	}
	return b, nil
}

func (r *AIBriefRepo) Save(_ context.Context, b domain.StoredBrief) (domain.StoredBrief, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.SaveErr != nil {
		return domain.StoredBrief{}, r.SaveErr
	}
	r.Saves++
	k := briefKey(b.OrderbookID, b.UTCDate, b.PromptVersion)
	if existing, ok := r.briefs[k]; ok {
		return existing, nil
	}
	r.briefs[k] = b
	return b, nil
}

// ---------------------------------------------------------------------------
// LLM
// ---------------------------------------------------------------------------

// LLM is a scriptable domain.LLM that records every request. Unset GenerateFn returns ErrNotConfigured;
// unset StreamFn calls Generate and emits its Content as one text event.
type LLM struct {
	mu         sync.Mutex
	Requests   []domain.LLMRequest
	GenerateFn func(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error)
	StreamFn   func(ctx context.Context, req domain.LLMRequest, onEvent func(domain.LLMEvent) error) (domain.LLMResponse, error)
}

func (l *LLM) Name() string { return "testutil" }

func (l *LLM) Generate(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	l.mu.Lock()
	l.Requests = append(l.Requests, req)
	fn := l.GenerateFn
	l.mu.Unlock()
	if fn == nil {
		return domain.LLMResponse{}, ErrNotConfigured
	}
	return fn(ctx, req)
}

func (l *LLM) Stream(ctx context.Context, req domain.LLMRequest, onEvent func(domain.LLMEvent) error) (domain.LLMResponse, error) {
	l.mu.Lock()
	fn := l.StreamFn
	l.mu.Unlock()
	if fn != nil {
		l.mu.Lock()
		l.Requests = append(l.Requests, req)
		l.mu.Unlock()
		return fn(ctx, req, onEvent)
	}
	resp, err := l.Generate(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := onEvent(domain.LLMEvent{Type: domain.LLMEventText, Text: resp.Content}); err != nil {
		return resp, err
	}
	return resp, nil
}

// Calls is the number of requests seen so far.
func (l *LLM) Calls() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.Requests)
}

// ---------------------------------------------------------------------------
// InsightsReviewRepository
// ---------------------------------------------------------------------------

// InsightsReviewRepo is an in-memory domain.InsightsReviewRepository.
type InsightsReviewRepo struct {
	mu        sync.Mutex
	Reviews   map[uint][]domain.InsightsReview
	NextID    uint
	CreateErr error
	ListErr   error
}

func NewInsightsReviewRepo() *InsightsReviewRepo {
	return &InsightsReviewRepo{Reviews: map[uint][]domain.InsightsReview{}}
}

func (r *InsightsReviewRepo) Create(_ context.Context, review *domain.InsightsReview, userID uint, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.CreateErr != nil {
		return r.CreateErr
	}
	if r.Reviews == nil {
		r.Reviews = map[uint][]domain.InsightsReview{}
	}
	r.NextID++
	review.ID = r.NextID
	r.Reviews[userID] = append([]domain.InsightsReview{*review}, r.Reviews[userID]...)
	return nil
}

func (r *InsightsReviewRepo) List(_ context.Context, userID uint, limit int) ([]domain.InsightsReview, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ListErr != nil {
		return nil, r.ListErr
	}
	items := r.Reviews[userID]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	out := make([]domain.InsightsReview, len(items))
	copy(out, items)
	return out, nil
}
