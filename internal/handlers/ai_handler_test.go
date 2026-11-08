package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/llm/fake"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
)

var aiNow = time.Date(2026, 11, 8, 9, 30, 0, 0, time.UTC)

func aiClock() time.Time { return aiNow }

type aiFixture struct {
	engine    *gin.Engine
	usage     *testutil.AIUsageRepo
	llm       *testutil.LLM
	knowledge *testutil.KnowledgeFake
}

func newAIFixture(llm *testutil.LLM, budget int64) aiFixture {
	usage := testutil.NewAIUsageRepo()
	gw := services.NewAIGateway(llm, usage, budget, time.Second, config.DefaultModelPrices(), aiClock)
	market := &testutil.MarketProvider{DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
		switch id {
		case "SIM-FJRD":
			return domain.InstrumentDetails{OrderbookID: id, Symbol: "FJRD", Name: "Fjord Robotics", Currency: "SEK", LastPrice: 100}, nil
		case "SIM-DOWN":
			return domain.InstrumentDetails{}, fmt.Errorf("%w: timeout", domain.ErrMarketDataUnavailable)
		}
		return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
	}}
	briefs := services.NewAIBriefService(gw, market, testutil.NewAIBriefRepo(), aiClock)
	knowledge := testutil.NewKnowledgeFake()
	firstSentence := "Slippage is the difference between the expected price of a trade and the price at which the trade is executed."
	knowledge.AddArticle(domain.Article{
		Slug:  "slippage",
		Title: "Slippage: Why Orders Fill Off-Price",
	}, []domain.Chunk{
		{
			ChunkID:      "slippage-what-is-slippage",
			ArticleSlug:  "slippage",
			ArticleTitle: "Slippage: Why Orders Fill Off-Price",
			Heading:      "What is Slippage?",
			Content:      firstSentence + " In fast-moving markets, orders can drift.",
		},
	})
	ask := services.NewAskService(gw, knowledge)
	return aiFixture{engine: newEngine(NewAIHandler(briefs, gw, ask, newAuth())), usage: usage, llm: llm, knowledge: knowledge}
}

func fakeLLM() *testutil.LLM { return &testutil.LLM{GenerateFn: fake.New().Generate} }

func failingLLM(err error) *testutil.LLM {
	return &testutil.LLM{GenerateFn: func(context.Context, domain.LLMRequest) (domain.LLMResponse, error) {
		return domain.LLMResponse{}, err
	}}
}

func TestAIBriefHandler(t *testing.T) {
	f := newAIFixture(fakeLLM(), 0)

	w := do(t, f.engine, http.MethodGet, "/api/v1/ai/instruments/SIM-FJRD/brief", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"orderbook_id", "generated_at", "model", "prompt_version", "cached", "disclaimer", "brief"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("body %s lacks %s", w.Body.String(), k)
		}
	}
	var body domain.InstrumentBrief
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Cached || body.Brief.Summary != "FAKE: Fjord Robotics (FJRD) trades at 100.00 SEK." ||
		body.Disclaimer != "AI-generated · educational, not financial advice" || body.GeneratedAt.Format(time.RFC3339) != "2026-11-08T09:30:00Z" {
		t.Fatalf("body = %+v", body)
	}

	w = do(t, f.engine, http.MethodGet, "/api/v1/ai/instruments/SIM-FJRD/brief", adminSession, nil)
	expectStatus(t, w, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.Cached {
		t.Fatalf("second user: %s", w.Body.String())
	}
}

func TestAIBriefHandlerErrors(t *testing.T) {
	cases := []struct {
		name     string
		llm      *testutil.LLM
		budget   int64
		path     string
		session  string
		wantCode int
		wantBody string
	}{
		{"no session", fakeLLM(), 0, "/api/v1/ai/instruments/SIM-FJRD/brief", "", http.StatusUnauthorized, domain.CodeUnauthenticated},
		{"unknown instrument", fakeLLM(), 0, "/api/v1/ai/instruments/SIM-NOPE/brief", memberSession, http.StatusNotFound, domain.CodeInstrumentNotFound},
		{"market down", fakeLLM(), 0, "/api/v1/ai/instruments/SIM-DOWN/brief", memberSession, http.StatusBadGateway, domain.CodeMarketDataUnavailable},
		{"refused", failingLLM(domain.ErrAIRefused), 0, "/api/v1/ai/instruments/SIM-FJRD/brief", memberSession, http.StatusUnprocessableEntity, domain.CodeAIRefused},
		{"provider down", failingLLM(fmt.Errorf("%w: 529", domain.ErrAIUnavailable)), 0, "/api/v1/ai/instruments/SIM-FJRD/brief", memberSession, http.StatusServiceUnavailable, domain.CodeAIUnavailable},
		{"unmapped error", failingLLM(errors.New("boom")), 0, "/api/v1/ai/instruments/SIM-FJRD/brief", memberSession, http.StatusServiceUnavailable, domain.CodeAIUnavailable},
		{"usage without session", fakeLLM(), 0, "/api/v1/ai/usage", "", http.StatusUnauthorized, domain.CodeUnauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAIFixture(tc.llm, tc.budget)
			w := do(t, f.engine, http.MethodGet, tc.path, tc.session, nil)
			expectStatus(t, w, tc.wantCode)
			var e domain.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Code != tc.wantBody || e.Error == "" {
				t.Fatalf("body = %s, want code %s", w.Body.String(), tc.wantBody)
			}
			if tc.wantCode == http.StatusNotFound && (len(f.usage.Snapshot()) != 0 || f.llm.Calls() != 0) {
				t.Fatal("an unknown instrument must not call the model or record usage")
			}
		})
	}
}

func TestAIBriefHandlerBudgetExceeded(t *testing.T) {
	f := newAIFixture(fakeLLM(), 5)
	f.usage.Records = append(f.usage.Records, domain.AIUsageRecord{UserID: 1, Feature: domain.AIFeatureInstrumentBrief, Usage: domain.LLMUsage{Input: 5}, CreatedAt: aiNow})
	w := do(t, f.engine, http.MethodGet, "/api/v1/ai/instruments/SIM-FJRD/brief", memberSession, nil)
	expectStatus(t, w, http.StatusTooManyRequests)
	var e domain.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Code != domain.CodeAIBudgetExceeded {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestAIUsageHandler(t *testing.T) {
	f := newAIFixture(fakeLLM(), 0)
	expectStatus(t, do(t, f.engine, http.MethodGet, "/api/v1/ai/instruments/SIM-FJRD/brief", memberSession, nil), http.StatusOK)

	cases := []struct {
		name      string
		session   string
		wantCalls int64
	}{
		{"the user who generated the brief", memberSession, 1},
		{"another user", adminSession, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.session == adminSession { // a cached brief adds no call
				expectStatus(t, do(t, f.engine, http.MethodGet, "/api/v1/ai/instruments/SIM-FJRD/brief", adminSession, nil), http.StatusOK)
			}
			w := do(t, f.engine, http.MethodGet, "/api/v1/ai/usage", tc.session, nil)
			expectStatus(t, w, http.StatusOK)
			var u domain.AIUsage
			if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
				t.Fatal(err)
			}
			var briefFeature domain.AIFeatureUsage
			for _, bf := range u.ByFeature {
				if bf.Feature == domain.AIFeatureInstrumentBrief {
					briefFeature = bf
				}
			}
			if u.Date != "2026-11-08" || u.BudgetTokens != domain.DefaultAIDailyTokenBudget || len(u.ByFeature) != 3 ||
				briefFeature.Feature != domain.AIFeatureInstrumentBrief || briefFeature.Calls != tc.wantCalls {
				t.Fatalf("usage = %s", w.Body.String())
			}
			if (tc.wantCalls > 0) != (u.UsedTokens > 0) || u.RemainingTokens != u.BudgetTokens-u.UsedTokens {
				t.Fatalf("usage tokens = %s", w.Body.String())
			}
		})
	}
}

func TestAIAskHandler(t *testing.T) {
	f := newAIFixture(fakeLLM(), 0)

	// Unauthenticated returns 401
	w := do(t, f.engine, http.MethodPost, "/api/v1/ai/ask", "", map[string]any{"question": "What is slippage?"})
	expectStatus(t, w, http.StatusUnauthorized)

	// Blank question returns 400
	w = do(t, f.engine, http.MethodPost, "/api/v1/ai/ask", memberSession, map[string]any{"question": "   "})
	expectStatus(t, w, http.StatusBadRequest)

	// Known topic returns 200 with citations
	w = do(t, f.engine, http.MethodPost, "/api/v1/ai/ask", memberSession, map[string]any{"question": "What is slippage?"})
	expectStatus(t, w, http.StatusOK)
	var resp domain.AskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Citations) == 0 {
		t.Fatalf("expected at least 1 citation, got 0")
	}
	if resp.Citations[0].ArticleSlug != "slippage" {
		t.Errorf("citation slug = %q, want 'slippage'", resp.Citations[0].ArticleSlug)
	}
	if resp.Disclaimer != domain.AIDisclaimer {
		t.Errorf("disclaimer = %q, want %q", resp.Disclaimer, domain.AIDisclaimer)
	}

	// 0 hits returns 200 with abstain answer and empty citations
	w = do(t, f.engine, http.MethodPost, "/api/v1/ai/ask", memberSession, map[string]any{"question": "zxqv qqqq"})
	expectStatus(t, w, http.StatusOK)
	var abstain domain.AskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &abstain); err != nil {
		t.Fatalf("decode abstain: %v", err)
	}
	if abstain.Answer != services.AbstainAnswer {
		t.Errorf("answer = %q, want %q", abstain.Answer, services.AbstainAnswer)
	}
	if len(abstain.Citations) != 0 {
		t.Errorf("citations = %v, want empty", abstain.Citations)
	}
}
