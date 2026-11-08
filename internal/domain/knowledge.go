package domain

import (
	"context"
	"errors"
	"time"
)

// Knowledge base error codes (story NW-15).
const (
	CodeArticleNotFound = "ARTICLE_NOT_FOUND"
)

var (
	// ErrArticleNotFound means no knowledge base article matches the requested slug. 404 ARTICLE_NOT_FOUND.
	ErrArticleNotFound = errors.New("article not found")
)

// ArticleSummary is the listing shape for knowledge base articles (#/components/schemas/ArticleSummary).
type ArticleSummary struct {
	Slug    string   `json:"slug"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

// Article is the full article shape (#/components/schemas/Article).
type Article struct {
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary"`
	Tags         []string  `json:"tags"`
	BodyMarkdown string    `json:"body_markdown"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Chunk is one segmented piece of an article used for retrieval.
type Chunk struct {
	ChunkID      string `json:"chunk_id"`
	ArticleSlug  string `json:"article_slug"`
	ArticleTitle string `json:"article_title"`
	Heading      string `json:"heading"`
	Content      string `json:"content"`
}

// SearchResult is one item returned by GET /learn/search (#/components/schemas/ArticleSearchResult).
type SearchResult struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	ChunkID string `json:"chunk_id"`
	Snippet string `json:"snippet"`
}

// Retriever abstracts semantic/full-text search over article chunks so vector search can be added later.
type Retriever interface {
	Search(ctx context.Context, query string, k int) ([]Chunk, error)
}

// KnowledgeRepository manages article persistence, full-text search, and seeding.
type KnowledgeRepository interface {
	Retriever
	ListArticles(ctx context.Context) ([]ArticleSummary, error)
	GetArticleBySlug(ctx context.Context, slug string) (*Article, error)
	SearchWithSnippets(ctx context.Context, query string, k int) ([]SearchResult, error)
}

// Citation is a chunk citation linked to an article (#/components/schemas/Citation).
type Citation struct {
	ArticleSlug  string `json:"article_slug"`
	ArticleTitle string `json:"article_title"`
	ChunkID      string `json:"chunk_id"`
	QuotedText   string `json:"quoted_text"`
}

// AskRequest is the body of POST /api/v1/ai/ask (#/components/schemas/AskRequest).
type AskRequest struct {
	Question string `json:"question"`
}

// AskResponse is the body of POST /api/v1/ai/ask (#/components/schemas/AskResponse).
type AskResponse struct {
	Answer     string     `json:"answer"`
	Citations  []Citation `json:"citations"`
	Model      string     `json:"model"`
	Disclaimer string     `json:"disclaimer"`
}
