package knowledge_test

import (
	"io/fs"
	"strings"
	"testing"

	"backend/internal/knowledge"
)

func TestParseArticle_Valid(t *testing.T) {
	raw := `---
slug: test-article
title: "Test Article Title"
summary: "A brief summary for tests."
tags: [trading, test]
---

## First Section

This is the first section of the test article. It contains some words.

## Second Section

This is the second section of the test article. More words here.
`

	parsed, err := knowledge.ParseArticle([]byte(raw))
	if err != nil {
		t.Fatalf("ParseArticle failed: %v", err)
	}

	if parsed.Slug != "test-article" {
		t.Errorf("slug = %q, want 'test-article'", parsed.Slug)
	}
	if parsed.Title != "Test Article Title" {
		t.Errorf("title = %q, want 'Test Article Title'", parsed.Title)
	}
	if len(parsed.Tags) != 2 || parsed.Tags[0] != "trading" {
		t.Errorf("tags = %v, want [trading test]", parsed.Tags)
	}
	if len(parsed.Chunks) != 2 {
		t.Fatalf("chunks count = %d, want 2", len(parsed.Chunks))
	}

	if parsed.Chunks[0].ChunkID != "test-article-first-section" {
		t.Errorf("chunk[0] id = %q, want 'test-article-first-section'", parsed.Chunks[0].ChunkID)
	}
	if parsed.Chunks[0].Heading != "First Section" {
		t.Errorf("chunk[0] heading = %q, want 'First Section'", parsed.Chunks[0].Heading)
	}
	if !strings.HasPrefix(parsed.Chunks[0].Content, "This is the first section") {
		t.Errorf("chunk[0] content = %q, want prefix 'This is the first section'", parsed.Chunks[0].Content)
	}

	if parsed.Chunks[1].ChunkID != "test-article-second-section" {
		t.Errorf("chunk[1] id = %q, want 'test-article-second-section'", parsed.Chunks[1].ChunkID)
	}
}

func TestParseArticle_Invalid(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "missing delimiters",
			content: "just raw markdown without front matter",
			wantErr: "missing YAML front matter",
		},
		{
			name:    "missing slug",
			content: "---\ntitle: \"No Slug\"\n---\nBody",
			wantErr: "missing required 'slug'",
		},
		{
			name:    "missing title",
			content: "---\nslug: no-title\n---\nBody",
			wantErr: "missing required 'title'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := knowledge.ParseArticle([]byte(tc.content))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestEmbeddedArticles_ParseAll(t *testing.T) {
	entries, err := fs.ReadDir(knowledge.ArticlesFS, "articles")
	if err != nil {
		t.Fatalf("ReadDir embedded articles: %v", err)
	}

	if len(entries) < 15 {
		t.Fatalf("embedded articles count = %d, want at least 15", len(entries))
	}

	slugs := make(map[string]bool)
	requiredSlugs := []string{
		"bid-ask-spread",
		"slippage",
		"commission",
		"limit-and-stop-orders",
		"diversification",
		"sharpe-ratio",
		"max-drawdown",
		"opportunity-cost",
		"disposition-effect",
	}

	for _, entry := range entries {
		data, err := fs.ReadFile(knowledge.ArticlesFS, "articles/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		parsed, err := knowledge.ParseArticle(data)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		if slugs[parsed.Slug] {
			t.Errorf("duplicate slug %q across articles", parsed.Slug)
		}
		slugs[parsed.Slug] = true

		if len(parsed.Chunks) == 0 {
			t.Errorf("article %q produced 0 chunks", parsed.Slug)
		}
		for _, ch := range parsed.Chunks {
			if ch.ChunkID == "" || ch.Content == "" {
				t.Errorf("article %q has empty chunk ID or content: %+v", parsed.Slug, ch)
			}
		}
	}

	for _, req := range requiredSlugs {
		if !slugs[req] {
			t.Errorf("required slug %q not found in embedded articles", req)
		}
	}
}
