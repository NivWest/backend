package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/testutil"

	"github.com/shopspring/decimal"
)

var gatewayNow = time.Date(2026, 11, 8, 14, 30, 0, 0, time.UTC)

func fixedNow() time.Time { return gatewayNow }

func newGateway(llm domain.LLM, usage *testutil.AIUsageRepo, budget int64) *AIGateway {
	return NewAIGateway(llm, usage, budget, time.Second, config.DefaultModelPrices(), fixedNow)
}

func okLLM(usage domain.LLMUsage, model string) *testutil.LLM {
	return &testutil.LLM{GenerateFn: func(context.Context, domain.LLMRequest) (domain.LLMResponse, error) {
		return domain.LLMResponse{Content: "ok", Usage: usage, Model: model, StopReason: domain.LLMStopEndTurn, Latency: 1500 * time.Millisecond}, nil
	}}
}

var briefRequest = domain.LLMRequest{Feature: domain.AIFeatureInstrumentBrief, PromptVersion: "instrument_brief.v1"}

func TestAIGatewayGenerateRecordsLedgerRow(t *testing.T) {
	cases := []struct {
		name       string
		resp       domain.LLMResponse
		err        error
		wantErr    error
		wantStatus string
		wantCost   string
		wantModel  string
	}{
		{
			name:       "ok on claude-opus-5-5 prices input, output and cache tokens",
			resp:       domain.LLMResponse{Model: "claude-opus-5-5", Usage: domain.LLMUsage{Input: 1000, Output: 500, CacheRead: 2000, CacheWrite: 100}},
			wantStatus: domain.AIUsageStatusOK,
			// 1000*4 + 500*20 + 2000*0.20 + 100*5 = 4000 + 10000 + 400 + 500 = 14900 per MTok → 0.0149 USD
			wantCost:  "0.0149",
			wantModel: "claude-opus-5-5",
		},
		{
			name:       "refusal is recorded as refused and returned",
			resp:       domain.LLMResponse{Model: "claude-opus-5-5", Usage: domain.LLMUsage{Input: 300}, StopReason: domain.LLMStopRefusal},
			err:        domain.ErrAIRefused,
			wantErr:    domain.ErrAIRefused,
			wantStatus: domain.AIUsageStatusRefused,
			wantCost:   "0.0012",
			wantModel:  "claude-opus-5-5",
		},
		{
			name:       "provider error is recorded as error",
			err:        fmt.Errorf("%w: status 529", domain.ErrAIUnavailable),
			wantErr:    domain.ErrAIUnavailable,
			wantStatus: domain.AIUsageStatusError,
			wantCost:   "0",
			wantModel:  "unknown",
		},
		{
			name:       "an unmapped error becomes ErrAIUnavailable",
			err:        context.DeadlineExceeded,
			wantErr:    domain.ErrAIUnavailable,
			wantStatus: domain.AIUsageStatusError,
			wantCost:   "0",
			wantModel:  "unknown",
		},
		{
			name:       "unknown model costs 0",
			resp:       domain.LLMResponse{Model: "some-new-model", Usage: domain.LLMUsage{Input: 1000, Output: 1000}},
			wantStatus: domain.AIUsageStatusOK,
			wantCost:   "0",
			wantModel:  "some-new-model",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := testutil.NewAIUsageRepo()
			llm := &testutil.LLM{GenerateFn: func(context.Context, domain.LLMRequest) (domain.LLMResponse, error) { return tc.resp, tc.err }}
			_, err := newGateway(llm, usage, 0).Generate(context.Background(), 7, briefRequest)
			if tc.wantErr == nil && err != nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			rows := usage.Snapshot()
			if len(rows) != 1 {
				t.Fatalf("ledger rows = %d, want 1", len(rows))
			}
			r := rows[0]
			if r.UserID != 7 || r.Feature != domain.AIFeatureInstrumentBrief || r.PromptVersion != "instrument_brief.v1" ||
				r.Status != tc.wantStatus || r.Model != tc.wantModel || !r.CreatedAt.Equal(gatewayNow) {
				t.Fatalf("row = %+v", r)
			}
			if !r.CostUSD.Equal(decimal.RequireFromString(tc.wantCost)) {
				t.Fatalf("cost = %s, want %s", r.CostUSD, tc.wantCost)
			}
			if r.Usage != tc.resp.Usage {
				t.Fatalf("usage = %+v, want %+v", r.Usage, tc.resp.Usage)
			}
		})
	}
}

func TestAIGatewayRecordsLatencyAndFallback(t *testing.T) {
	usage := testutil.NewAIUsageRepo()
	llm := &testutil.LLM{GenerateFn: func(context.Context, domain.LLMRequest) (domain.LLMResponse, error) {
		return domain.LLMResponse{Model: "claude-opus-4-8", FallbackModel: "claude-opus-4-8", Latency: 1500 * time.Millisecond}, nil
	}}
	if _, err := newGateway(llm, usage, 0).Generate(context.Background(), 1, briefRequest); err != nil {
		t.Fatal(err)
	}
	r := usage.Snapshot()[0]
	if r.LatencyMs != 1500 || r.FallbackModel != "claude-opus-4-8" {
		t.Fatalf("row = %+v, want latency 1500 ms and the fallback model", r)
	}
}

func TestAIGatewayBudget(t *testing.T) {
	cases := []struct {
		name     string
		previous []domain.AIUsageRecord
		wantErr  error
	}{
		{"fresh user", nil, nil},
		{"one token left", []domain.AIUsageRecord{{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 60, Output: 39}, CreatedAt: gatewayNow}}, nil},
		{"exactly used up", []domain.AIUsageRecord{{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 60, Output: 40}, CreatedAt: gatewayNow}}, domain.ErrAIBudgetExceeded},
		{"cache tokens count", []domain.AIUsageRecord{{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{CacheRead: 70, CacheWrite: 30}, CreatedAt: gatewayNow}}, domain.ErrAIBudgetExceeded},
		{"yesterday does not count", []domain.AIUsageRecord{{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 500}, CreatedAt: gatewayNow.AddDate(0, 0, -1)}}, nil},
		{"another user does not count", []domain.AIUsageRecord{{UserID: 2, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 500}, CreatedAt: gatewayNow}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := testutil.NewAIUsageRepo()
			usage.Records = append(usage.Records, tc.previous...)
			llm := okLLM(domain.LLMUsage{Input: 1}, "fake")
			_, err := newGateway(llm, usage, 100).Generate(context.Background(), 1, briefRequest)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if llm.Calls() != 0 {
					t.Fatal("the budget is checked before the call: the model must not be called")
				}
				if n := len(usage.Snapshot()); n != len(tc.previous) {
					t.Fatalf("a refused budget check must not record a row (rows %d)", n)
				}
			}
		})
	}
}

func TestAIGatewayUsageListsEveryFeature(t *testing.T) {
	usage := testutil.NewAIUsageRepo()
	g := newGateway(okLLM(domain.LLMUsage{}, "fake"), usage, 1000)
	empty, err := g.Usage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Date != "2026-11-08" || empty.BudgetTokens != 1000 || empty.UsedTokens != 0 || empty.RemainingTokens != 1000 ||
		len(empty.ByFeature) != 2 || empty.ByFeature[0] != (domain.AIFeatureUsage{Feature: domain.AIFeatureInstrumentBrief}) ||
		empty.ByFeature[1] != (domain.AIFeatureUsage{Feature: domain.AIFeatureKnowledgeAsk}) {
		t.Fatalf("empty usage = %+v", empty)
	}

	usage.Records = []domain.AIUsageRecord{
		{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 700, Output: 200, CacheRead: 100}, CreatedAt: gatewayNow},
		{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 300, Output: 50}, CreatedAt: gatewayNow},
	}
	u, err := g.Usage(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	f := u.ByFeature[0]
	if u.UsedTokens != 1350 || u.RemainingTokens != 0 || f.Calls != 2 || f.InputTokens != 1000 || f.OutputTokens != 250 {
		t.Fatalf("usage = %+v (remaining is clamped at 0)", u)
	}
}

func TestAIGatewayTimeout(t *testing.T) {
	usage := testutil.NewAIUsageRepo()
	llm := &testutil.LLM{GenerateFn: func(ctx context.Context, _ domain.LLMRequest) (domain.LLMResponse, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("the gateway must give the provider a deadline")
		}
		<-ctx.Done()
		return domain.LLMResponse{}, ctx.Err()
	}}
	g := NewAIGateway(llm, usage, 0, 20*time.Millisecond, nil, nil)
	_, err := g.Generate(context.Background(), 1, briefRequest)
	if !errors.Is(err, domain.ErrAIUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want ErrAIUnavailable wrapping the deadline", err)
	}
	if rows := usage.Snapshot(); len(rows) != 1 || rows[0].Status != domain.AIUsageStatusError {
		t.Fatalf("rows = %+v, want one error row", rows)
	}
}

func TestAIGatewayLedgerFailureDoesNotFailTheCall(t *testing.T) {
	usage := testutil.NewAIUsageRepo()
	usage.RecordErr = errors.New("db down")
	resp, err := newGateway(okLLM(domain.LLMUsage{Input: 1}, "fake"), usage, 0).Generate(context.Background(), 1, briefRequest)
	if err != nil || resp.Content != "ok" {
		t.Fatalf("resp, err = %+v, %v", resp, err)
	}
}

func TestAIGatewayUsageReadError(t *testing.T) {
	usage := testutil.NewAIUsageRepo()
	usage.DailyErr = errors.New("db down")
	llm := okLLM(domain.LLMUsage{}, "fake")
	if _, err := newGateway(llm, usage, 0).Generate(context.Background(), 1, briefRequest); err == nil || errors.Is(err, domain.ErrAIBudgetExceeded) {
		t.Fatalf("err = %v, want the read error", err)
	}
	if llm.Calls() != 0 {
		t.Fatal("no call without a budget check")
	}
}

func TestAIGatewayStream(t *testing.T) {
	usage := testutil.NewAIUsageRepo()
	var got string
	_, err := newGateway(okLLM(domain.LLMUsage{Input: 3}, "fake"), usage, 0).Stream(context.Background(), 1, briefRequest, func(e domain.LLMEvent) error {
		got += e.Text
		return nil
	})
	if err != nil || got != "ok" || len(usage.Snapshot()) != 1 {
		t.Fatalf("stream = %q, %v, rows %d", got, err, len(usage.Snapshot()))
	}
}
