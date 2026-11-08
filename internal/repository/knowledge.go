package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"backend/internal/domain"
	"backend/internal/models"

	"gorm.io/gorm"
)

type knowledgeRepo struct {
	db *gorm.DB
}

var _ domain.KnowledgeRepository = (*knowledgeRepo)(nil)

// NewKnowledgeRepository creates a GORM-backed domain.KnowledgeRepository (story NW-15).
func NewKnowledgeRepository(db *gorm.DB) domain.KnowledgeRepository {
	return &knowledgeRepo{db: db}
}

func (r *knowledgeRepo) ListArticles(ctx context.Context) ([]domain.ArticleSummary, error) {
	var rows []models.KBArticle
	if err := r.db.WithContext(ctx).Order("title ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list articles: %w", err)
	}
	out := make([]domain.ArticleSummary, len(rows))
	for i, row := range rows {
		var tags []string
		_ = json.Unmarshal([]byte(row.Tags), &tags)
		out[i] = domain.ArticleSummary{
			Slug:    row.Slug,
			Title:   row.Title,
			Summary: row.Summary,
			Tags:    tags,
		}
	}
	return out, nil
}

func (r *knowledgeRepo) GetArticleBySlug(ctx context.Context, slug string) (*domain.Article, error) {
	var row models.KBArticle
	err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrArticleNotFound
		}
		return nil, fmt.Errorf("get article by slug: %w", err)
	}
	var tags []string
	_ = json.Unmarshal([]byte(row.Tags), &tags)
	return &domain.Article{
		Slug:         row.Slug,
		Title:        row.Title,
		Summary:      row.Summary,
		Tags:         tags,
		BodyMarkdown: row.BodyMarkdown,
		UpdatedAt:    row.UpdatedAt,
	}, nil
}

func (r *knowledgeRepo) Search(ctx context.Context, query string, k int) ([]domain.Chunk, error) {
	if k <= 0 {
		k = 5
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	var rows []struct {
		ChunkID      string `gorm:"column:chunk_id"`
		ArticleSlug  string `gorm:"column:article_slug"`
		ArticleTitle string `gorm:"column:article_title"`
		Heading      string `gorm:"column:heading"`
		Content      string `gorm:"column:content"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT chunk_id, article_slug, article_title, heading, content
		FROM kb_chunks, websearch_to_tsquery('english', ?) q
		WHERE search @@ q AND ts_rank_cd(search, q) > 0.001
		ORDER BY ts_rank_cd(search, q) DESC
		LIMIT ?
	`, q, k).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	chunks := make([]domain.Chunk, len(rows))
	for i, row := range rows {
		chunks[i] = domain.Chunk{
			ChunkID:      row.ChunkID,
			ArticleSlug:  row.ArticleSlug,
			ArticleTitle: row.ArticleTitle,
			Heading:      row.Heading,
			Content:      row.Content,
		}
	}
	return chunks, nil
}

func (r *knowledgeRepo) SearchWithSnippets(ctx context.Context, query string, k int) ([]domain.SearchResult, error) {
	if k <= 0 {
		k = 5
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	var rows []struct {
		ArticleSlug  string `gorm:"column:article_slug"`
		ArticleTitle string `gorm:"column:article_title"`
		ChunkID      string `gorm:"column:chunk_id"`
		Snippet      string `gorm:"column:snippet"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			article_slug,
			article_title,
			chunk_id,
			ts_headline('english', content, q, 'StartSel=<b>, StopSel=</b>, MaxWords=35, MinWords=15') AS snippet
		FROM kb_chunks, websearch_to_tsquery('english', ?) q
		WHERE search @@ q AND ts_rank_cd(search, q) > 0.001
		ORDER BY ts_rank_cd(search, q) DESC
		LIMIT ?
	`, q, k).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("search snippets: %w", err)
	}
	results := make([]domain.SearchResult, len(rows))
	for i, row := range rows {
		results[i] = domain.SearchResult{
			Slug:    row.ArticleSlug,
			Title:   row.ArticleTitle,
			ChunkID: row.ChunkID,
			Snippet: row.Snippet,
		}
	}
	return results, nil
}
