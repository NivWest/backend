package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/services"
	"backend/internal/testutil"
)

func TestKnowledgeService_ListArticles(t *testing.T) {
	fake := testutil.NewKnowledgeFake()
	fake.AddArticle(domain.Article{
		Slug:    "slippage",
		Title:   "Slippage Article",
		Summary: "Summary",
		Tags:    []string{"execution"},
	}, nil)

	svc := services.NewKnowledgeService(fake)
	items, err := svc.ListArticles(context.Background())
	if err != nil {
		t.Fatalf("ListArticles: %v", err)
	}
	if len(items) != 1 || items[0].Slug != "slippage" {
		t.Errorf("got %v, want 1 item with slug 'slippage'", items)
	}
}

func TestKnowledgeService_GetArticle(t *testing.T) {
	fake := testutil.NewKnowledgeFake()
	now := time.Now().UTC()
	fake.AddArticle(domain.Article{
		Slug:         "slippage",
		Title:        "Slippage",
		Summary:      "Summary",
		Tags:         []string{"execution"},
		BodyMarkdown: "# Content",
		UpdatedAt:    now,
	}, nil)

	svc := services.NewKnowledgeService(fake)

	// Found
	art, err := svc.GetArticle(context.Background(), "slippage")
	if err != nil {
		t.Fatalf("GetArticle slippage: %v", err)
	}
	if art.Slug != "slippage" || art.Title != "Slippage" {
		t.Errorf("got article %+v", art)
	}

	// Not found
	_, err = svc.GetArticle(context.Background(), "unknown-slug")
	if !errors.Is(err, domain.ErrArticleNotFound) {
		t.Errorf("got error %v, want ErrArticleNotFound", err)
	}

	// Blank slug
	_, err = svc.GetArticle(context.Background(), "   ")
	if !errors.Is(err, domain.ErrArticleNotFound) {
		t.Errorf("got error %v, want ErrArticleNotFound", err)
	}
}

func TestKnowledgeService_Search(t *testing.T) {
	fake := testutil.NewKnowledgeFake()
	fake.AddArticle(domain.Article{
		Slug:  "slippage",
		Title: "Slippage: Why Orders Fill Off-Price",
	}, []domain.Chunk{
		{
			ChunkID:      "slippage-what-is-slippage",
			ArticleSlug:  "slippage",
			ArticleTitle: "Slippage: Why Orders Fill Off-Price",
			Heading:      "What is Slippage?",
			Content:      "Slippage is the difference between expected price and executed price.",
		},
	})

	svc := services.NewKnowledgeService(fake)

	// Valid search
	results, err := svc.Search(context.Background(), "slippage")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("got 0 results, want at least 1")
	}
	if results[0].Slug != "slippage" {
		t.Errorf("results[0].Slug = %q, want 'slippage'", results[0].Slug)
	}

	// Blank search query returns ErrBlankSearchQuery
	_, err = svc.Search(context.Background(), "   ")
	if !errors.Is(err, services.ErrBlankSearchQuery) {
		t.Errorf("got error %v, want ErrBlankSearchQuery", err)
	}
}
