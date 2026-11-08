package services_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/llm/fake"
	"backend/internal/services"
	"backend/internal/testutil"
)

func TestAskService_Validation(t *testing.T) {
	fakeRepo := testutil.NewKnowledgeFake()
	usageRepo := testutil.NewAIUsageRepo()
	gw := services.NewAIGateway(fake.New(), usageRepo, 100000, time.Second, config.DefaultModelPrices(), nil)
	svc := services.NewAskService(gw, fakeRepo)

	ctx := context.Background()

	// Empty question
	_, err := svc.Ask(ctx, 1, "   ")
	var valErr *domain.ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("empty question error = %v, want ValidationError", err)
	}

	// Over 500 chars
	longQuestion := strings.Repeat("a", 501)
	_, err = svc.Ask(ctx, 1, longQuestion)
	if !errors.As(err, &valErr) {
		t.Fatalf("long question error = %v, want ValidationError", err)
	}
}

func TestAskService_AbstainWhenZeroHits(t *testing.T) {
	fakeRepo := testutil.NewKnowledgeFake() // empty knowledge base
	usageRepo := testutil.NewAIUsageRepo()
	gw := services.NewAIGateway(fake.New(), usageRepo, 100000, time.Second, config.DefaultModelPrices(), nil)
	svc := services.NewAskService(gw, fakeRepo)

	ctx := context.Background()
	resp, err := svc.Ask(ctx, 1, "What is slippage?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	if resp.Answer != services.AbstainAnswer {
		t.Errorf("answer = %q, want %q", resp.Answer, services.AbstainAnswer)
	}
	if len(resp.Citations) != 0 {
		t.Errorf("citations = %v, want empty", resp.Citations)
	}
	if resp.Disclaimer != domain.AIDisclaimer {
		t.Errorf("disclaimer = %q, want %q", resp.Disclaimer, domain.AIDisclaimer)
	}

	// Verify gateway was NOT called (0 ledger records)
	usage, err := gw.Usage(ctx, 1)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if usage.UsedTokens != 0 {
		t.Errorf("used tokens = %d, want 0 (LLM should not be called)", usage.UsedTokens)
	}
	for _, f := range usage.ByFeature {
		if f.Feature == domain.AIFeatureKnowledgeAsk && f.Calls != 0 {
			t.Errorf("calls = %d, want 0", f.Calls)
		}
	}
}

func TestAskService_SuccessWithCitations(t *testing.T) {
	fakeRepo := testutil.NewKnowledgeFake()
	firstSentence := "Slippage is the difference between the expected price of a trade and the price at which the trade is executed."
	fakeRepo.AddArticle(domain.Article{
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

	usageRepo := testutil.NewAIUsageRepo()
	gw := services.NewAIGateway(fake.New(), usageRepo, 100000, time.Second, config.DefaultModelPrices(), nil)
	svc := services.NewAskService(gw, fakeRepo)

	ctx := context.Background()
	resp, err := svc.Ask(ctx, 1, "What is slippage?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	wantPrefix := "FAKE: According to \"Slippage: Why Orders Fill Off-Price\":"
	if !strings.HasPrefix(resp.Answer, wantPrefix) {
		t.Errorf("answer = %q, want prefix %q", resp.Answer, wantPrefix)
	}
	if len(resp.Citations) != 1 {
		t.Fatalf("citations count = %d, want 1", len(resp.Citations))
	}

	c := resp.Citations[0]
	if c.ArticleSlug != "slippage" {
		t.Errorf("citation slug = %q, want 'slippage'", c.ArticleSlug)
	}
	if c.ArticleTitle != "Slippage: Why Orders Fill Off-Price" {
		t.Errorf("citation title = %q, want 'Slippage: Why Orders Fill Off-Price'", c.ArticleTitle)
	}
	if c.ChunkID != "slippage-what-is-slippage" {
		t.Errorf("citation chunk_id = %q, want 'slippage-what-is-slippage'", c.ChunkID)
	}
	if c.QuotedText != firstSentence {
		t.Errorf("quoted_text = %q, want %q", c.QuotedText, firstSentence)
	}

	// Verify gateway recorded the call
	usage, err := gw.Usage(ctx, 1)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	var found bool
	for _, f := range usage.ByFeature {
		if f.Feature == domain.AIFeatureKnowledgeAsk {
			found = true
			if f.Calls != 1 {
				t.Errorf("knowledge_ask calls = %d, want 1", f.Calls)
			}
		}
	}
	if !found {
		t.Errorf("knowledge_ask feature not in usage breakdown")
	}
}
