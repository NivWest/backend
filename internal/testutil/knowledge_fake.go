package testutil

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"backend/internal/domain"
)

// KnowledgeFake is an in-memory domain.KnowledgeRepository for unit tests (story NW-15).
type KnowledgeFake struct {
	mu       sync.Mutex
	Articles map[string]domain.Article
	Chunks   []domain.Chunk
	Err      error
}

var _ domain.KnowledgeRepository = (*KnowledgeFake)(nil)

func NewKnowledgeFake() *KnowledgeFake {
	return &KnowledgeFake{
		Articles: make(map[string]domain.Article),
	}
}

func (f *KnowledgeFake) AddArticle(a domain.Article, chunks []domain.Chunk) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Articles[a.Slug] = a
	f.Chunks = append(f.Chunks, chunks...)
}

func (f *KnowledgeFake) ListArticles(ctx context.Context) ([]domain.ArticleSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	var out []domain.ArticleSummary
	for _, a := range f.Articles {
		out = append(out, domain.ArticleSummary{
			Slug:    a.Slug,
			Title:   a.Title,
			Summary: a.Summary,
			Tags:    a.Tags,
		})
	}
	return out, nil
}

func (f *KnowledgeFake) GetArticleBySlug(ctx context.Context, slug string) (*domain.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	a, ok := f.Articles[slug]
	if !ok {
		return nil, domain.ErrArticleNotFound
	}
	return &a, nil
}

func (f *KnowledgeFake) Search(ctx context.Context, query string, k int) ([]domain.Chunk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	terms := strings.Fields(q)
	var out []domain.Chunk
	for _, ch := range f.Chunks {
		haystack := strings.ToLower(ch.ArticleTitle + " " + ch.Heading + " " + ch.Content)
		matched := true
		for _, t := range terms {
			if !strings.Contains(haystack, t) {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, ch)
			if k > 0 && len(out) >= k {
				break
			}
		}
	}
	return out, nil
}

func (f *KnowledgeFake) SearchWithSnippets(ctx context.Context, query string, k int) ([]domain.SearchResult, error) {
	chunks, err := f.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}
	var out []domain.SearchResult
	for _, ch := range chunks {
		snippet := ch.Content
		if len(snippet) > 150 {
			snippet = snippet[:150] + "..."
		}
		out = append(out, domain.SearchResult{
			Slug:    ch.ArticleSlug,
			Title:   ch.ArticleTitle,
			ChunkID: ch.ChunkID,
			Snippet: fmt.Sprintf("<b>%s</b>: %s", ch.Heading, snippet),
		})
	}
	return out, nil
}
