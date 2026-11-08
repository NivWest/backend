package handlers

import (
	"errors"
	"net/http"
	"strings"

	"backend/internal/domain"
	"backend/internal/services"

	"github.com/gin-gonic/gin"
)

// LearnHandler serves public knowledge base routes under /learn (story NW-15).
type LearnHandler struct {
	knowledge *services.KnowledgeService
}

func NewLearnHandler(knowledge *services.KnowledgeService) *LearnHandler {
	return &LearnHandler{knowledge: knowledge}
}

func (h *LearnHandler) Register(v1 *gin.RouterGroup) {
	learn := v1.Group("/learn")
	learn.GET("/articles", h.ListArticles)
	learn.GET("/articles/:slug", h.GetArticle)
	learn.GET("/search", h.Search)
}

// ListArticles handles GET /learn/articles.
func (h *LearnHandler) ListArticles(c *gin.Context) {
	items, err := h.knowledge.ListArticles(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal error", Code: domain.CodeInternal})
		return
	}
	if items == nil {
		items = []domain.ArticleSummary{}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GetArticle handles GET /learn/articles/:slug.
func (h *LearnHandler) GetArticle(c *gin.Context) {
	slug := c.Param("slug")
	article, err := h.knowledge.GetArticle(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, domain.ErrArticleNotFound) {
			c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: "article not found", Code: domain.CodeArticleNotFound})
			return
		}
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal error", Code: domain.CodeInternal})
		return
	}
	c.JSON(http.StatusOK, article)
}

// Search handles GET /learn/search?q=.
func (h *LearnHandler) Search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "search query cannot be blank", Code: domain.CodeValidation})
		return
	}
	results, err := h.knowledge.Search(c.Request.Context(), q)
	if err != nil {
		if errors.Is(err, services.ErrBlankSearchQuery) {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "search query cannot be blank", Code: domain.CodeValidation})
			return
		}
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal error", Code: domain.CodeInternal})
		return
	}
	if results == nil {
		results = []domain.SearchResult{}
	}
	c.JSON(http.StatusOK, gin.H{"items": results})
}
