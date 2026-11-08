package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/handlers"
	"backend/internal/services"
	"backend/internal/testutil"

	"github.com/gin-gonic/gin"
)

func setupLearnRouter(fake *testutil.KnowledgeFake) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	svc := services.NewKnowledgeService(fake)
	h := handlers.NewLearnHandler(svc)
	h.Register(v1)
	return r
}

func TestLearnHandler_ListArticles(t *testing.T) {
	fake := testutil.NewKnowledgeFake()
	fake.AddArticle(domain.Article{
		Slug:    "slippage",
		Title:   "Slippage",
		Summary: "Summary",
		Tags:    []string{"execution"},
	}, nil)

	r := setupLearnRouter(fake)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/learn/articles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Items []domain.ArticleSummary `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Slug != "slippage" {
		t.Errorf("got items %v", body.Items)
	}
}

func TestLearnHandler_GetArticle(t *testing.T) {
	fake := testutil.NewKnowledgeFake()
	fake.AddArticle(domain.Article{
		Slug:         "slippage",
		Title:        "Slippage",
		Summary:      "Summary",
		Tags:         []string{"execution"},
		BodyMarkdown: "# Slippage content",
		UpdatedAt:    time.Now().UTC(),
	}, nil)

	r := setupLearnRouter(fake)

	// 200 OK
	req := httptest.NewRequest(http.MethodGet, "/api/v1/learn/articles/slippage", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var art domain.Article
	if err := json.Unmarshal(w.Body.Bytes(), &art); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if art.Slug != "slippage" {
		t.Errorf("slug = %q, want 'slippage'", art.Slug)
	}

	// 404 ARTICLE_NOT_FOUND
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/learn/articles/unknown", nil)
	w404 := httptest.NewRecorder()
	r.ServeHTTP(w404, req404)

	if w404.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w404.Code)
	}
	var errResp domain.ErrorResponse
	if err := json.Unmarshal(w404.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if errResp.Code != domain.CodeArticleNotFound {
		t.Errorf("code = %q, want %q", errResp.Code, domain.CodeArticleNotFound)
	}
}

func TestLearnHandler_Search(t *testing.T) {
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

	r := setupLearnRouter(fake)

	// Valid query
	req := httptest.NewRequest(http.MethodGet, "/api/v1/learn/search?q=slippage", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body struct {
		Items []domain.SearchResult `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) == 0 || body.Items[0].Slug != "slippage" {
		t.Errorf("search items: %v", body.Items)
	}

	// Blank query -> 400 VALIDATION_ERROR
	req400 := httptest.NewRequest(http.MethodGet, "/api/v1/learn/search?q=", nil)
	w400 := httptest.NewRecorder()
	r.ServeHTTP(w400, req400)

	if w400.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w400.Code)
	}
	var errResp domain.ErrorResponse
	if err := json.Unmarshal(w400.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if errResp.Code != domain.CodeValidation {
		t.Errorf("code = %q, want %q", errResp.Code, domain.CodeValidation)
	}
}
