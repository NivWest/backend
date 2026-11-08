package services

import (
	"context"
	"errors"
	"strings"

	"backend/internal/domain"
)

var (
	// ErrBlankSearchQuery indicates a blank or empty search query.
	ErrBlankSearchQuery = errors.New("search query cannot be blank")
)

// KnowledgeService manages knowledge base article retrieval and search (story NW-15).
type KnowledgeService struct {
	repo domain.KnowledgeRepository
}

func NewKnowledgeService(repo domain.KnowledgeRepository) *KnowledgeService {
	return &KnowledgeService{repo: repo}
}

func (s *KnowledgeService) ListArticles(ctx context.Context) ([]domain.ArticleSummary, error) {
	return s.repo.ListArticles(ctx)
}

func (s *KnowledgeService) GetArticle(ctx context.Context, slug string) (*domain.Article, error) {
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" {
		return nil, domain.ErrArticleNotFound
	}
	return s.repo.GetArticleBySlug(ctx, trimmed)
}

func (s *KnowledgeService) Search(ctx context.Context, query string) ([]domain.SearchResult, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, ErrBlankSearchQuery
	}
	return s.repo.SearchWithSnippets(ctx, trimmed, 5)
}
