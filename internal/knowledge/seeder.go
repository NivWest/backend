package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"time"

	"backend/internal/models"

	"gorm.io/gorm"
)

// Seed loads embedded articles into kb_articles and kb_chunks idempotently by content hash (story NW-15).
func Seed(ctx context.Context, db *gorm.DB) error {
	return SeedFromFS(ctx, db, ArticlesFS, "articles")
}

// SeedFromFS loads articles from a filesystem directory into the database.
func SeedFromFS(ctx context.Context, db *gorm.DB, fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read articles dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !hasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := fmt.Sprintf("%s/%s", dir, entry.Name())
		data, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return fmt.Errorf("read article file %s: %w", filePath, err)
		}

		parsed, err := ParseArticle(data)
		if err != nil {
			return fmt.Errorf("parse article %s: %w", filePath, err)
		}

		if err := seedArticle(ctx, db, parsed); err != nil {
			return fmt.Errorf("seed article %s: %w", parsed.Slug, err)
		}
	}

	return nil
}

func seedArticle(ctx context.Context, db *gorm.DB, a *ParsedArticle) error {
	tagsJSON, err := json.Marshal(a.Tags)
	if err != nil {
		tagsJSON = []byte("[]")
	}

	now := time.Now().UTC()

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing models.KBArticle
		err := tx.Where("slug = ?", a.Slug).First(&existing).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("find article: %w", err)
		}

		if err == gorm.ErrRecordNotFound {
			art := models.KBArticle{
				Slug:         a.Slug,
				Title:        a.Title,
				Summary:      a.Summary,
				Tags:         string(tagsJSON),
				BodyMarkdown: a.BodyMarkdown,
				ContentHash:  a.ContentHash,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			if err := tx.Create(&art).Error; err != nil {
				return fmt.Errorf("create article: %w", err)
			}
		} else if existing.ContentHash != a.ContentHash {
			updates := map[string]any{
				"title":         a.Title,
				"summary":       a.Summary,
				"tags":          string(tagsJSON),
				"body_markdown": a.BodyMarkdown,
				"content_hash":  a.ContentHash,
				"updated_at":    now,
			}
			if err := tx.Model(&existing).Updates(updates).Error; err != nil {
				return fmt.Errorf("update article: %w", err)
			}
		}

		var currentChunkIDs []string
		for _, ch := range a.Chunks {
			currentChunkIDs = append(currentChunkIDs, ch.ChunkID)
			h := sha256.Sum256([]byte(ch.Content))
			chunkHash := hex.EncodeToString(h[:])

			var existingChunk models.KBChunk
			chErr := tx.Where("chunk_id = ?", ch.ChunkID).First(&existingChunk).Error
			if chErr != nil && chErr != gorm.ErrRecordNotFound {
				return fmt.Errorf("find chunk %s: %w", ch.ChunkID, chErr)
			}

			if chErr == gorm.ErrRecordNotFound {
				row := models.KBChunk{
					ChunkID:      ch.ChunkID,
					ArticleSlug:  ch.ArticleSlug,
					ArticleTitle: ch.ArticleTitle,
					Heading:      ch.Heading,
					Content:      ch.Content,
					ContentHash:  chunkHash,
					CreatedAt:    now,
					UpdatedAt:    now,
				}
				if err := tx.Create(&row).Error; err != nil {
					return fmt.Errorf("create chunk %s: %w", ch.ChunkID, err)
				}
			} else if existingChunk.ContentHash != chunkHash || existingChunk.Heading != ch.Heading || existingChunk.ArticleTitle != ch.ArticleTitle {
				updates := map[string]any{
					"article_title": ch.ArticleTitle,
					"heading":       ch.Heading,
					"content":       ch.Content,
					"content_hash":  chunkHash,
					"updated_at":    now,
				}
				if err := tx.Model(&existingChunk).Updates(updates).Error; err != nil {
					return fmt.Errorf("update chunk %s: %w", ch.ChunkID, err)
				}
			}
		}

		// Prune any deleted chunks for this article
		if len(currentChunkIDs) > 0 {
			if err := tx.Where("article_slug = ? AND chunk_id NOT IN ?", a.Slug, currentChunkIDs).Delete(&models.KBChunk{}).Error; err != nil {
				return fmt.Errorf("prune obsolete chunks: %w", err)
			}
		}

		return nil
	})
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
