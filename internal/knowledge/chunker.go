package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"backend/internal/domain"

	"github.com/goccy/go-yaml"
)

var (
	frontMatterRegex = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n(.*)$`)
	nonAlphanumeric  = regexp.MustCompile(`[^a-z0-9]+`)
)

// FrontMatter holds the YAML header of a knowledge article.
type FrontMatter struct {
	Slug    string   `yaml:"slug"`
	Title   string   `yaml:"title"`
	Summary string   `yaml:"summary"`
	Tags    []string `yaml:"tags"`
}

// ParsedArticle is a completely parsed article with chunks and content hash.
type ParsedArticle struct {
	FrontMatter
	BodyMarkdown string
	Chunks       []domain.Chunk
	ContentHash  string
}

// ParseArticle parses a raw markdown file with YAML front matter and chunks it.
func ParseArticle(raw []byte) (*ParsedArticle, error) {
	content := string(raw)
	matches := frontMatterRegex.FindStringSubmatch(content)
	if len(matches) < 3 {
		return nil, fmt.Errorf("article missing YAML front matter delimiters (---)")
	}

	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(matches[1]), &fm); err != nil {
		return nil, fmt.Errorf("parse YAML front matter: %w", err)
	}

	if fm.Slug == "" {
		return nil, fmt.Errorf("article front matter missing required 'slug'")
	}
	if fm.Title == "" {
		return nil, fmt.Errorf("article front matter missing required 'title'")
	}

	body := strings.TrimSpace(matches[2])
	h := sha256.Sum256(raw)
	contentHash := hex.EncodeToString(h[:])

	chunks := ChunkMarkdown(fm.Slug, fm.Title, body)

	return &ParsedArticle{
		FrontMatter:  fm,
		BodyMarkdown: body,
		Chunks:       chunks,
		ContentHash:  contentHash,
	}, nil
}

// ChunkMarkdown splits markdown at '## ' headings into domain.Chunk objects.
func ChunkMarkdown(articleSlug, articleTitle, body string) []domain.Chunk {
	lines := strings.Split(body, "\n")
	var chunks []domain.Chunk

	currentHeading := "Overview"
	var currentLines []string

	flush := func() {
		text := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if text != "" {
			headingSlug := Slugify(currentHeading)
			chunkID := fmt.Sprintf("%s-%s", articleSlug, headingSlug)
			chunks = append(chunks, domain.Chunk{
				ChunkID:      chunkID,
				ArticleSlug:  articleSlug,
				ArticleTitle: articleTitle,
				Heading:      currentHeading,
				Content:      text,
			})
		}
		currentLines = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			currentHeading = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		} else {
			currentLines = append(currentLines, line)
		}
	}
	flush()

	return chunks
}

// Slugify converts a string to a lower-case hyphen-separated identifier.
func Slugify(s string) string {
	s = strings.ToLower(s)
	s = nonAlphanumeric.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
