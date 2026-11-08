package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newOpenAPIEngine(spec string) *gin.Engine {
	h := NewOpenAPIHandler([]byte(spec))
	r := gin.New()
	api := r.Group("/api")
	h.Register(api.Group("/v1"))
	h.RegisterDocs(api)
	return r
}

func TestOpenAPIHandler(t *testing.T) {
	const spec = "openapi: 3.1.0\ninfo:\n  title: TradeEdu API\n"

	tests := []struct {
		name        string
		path        string
		wantType    string
		wantBody    []string
		wantHeaders map[string]string
	}{
		{
			name:     "spec is served verbatim as YAML",
			path:     "/api/v1/openapi.yaml",
			wantType: OpenAPIContentType,
			wantBody: []string{spec},
		},
		{
			name:     "docs page loads the pinned Scalar build with SRI and points it at the served spec",
			path:     "/api/docs",
			wantType: "text/html; charset=utf-8",
			wantBody: []string{
				"<title>TradeEdu API reference</title>",
				`src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@` + scalarVersion + `/dist/browser/standalone.js"`,
				`integrity="` + scalarSRI + `"`,
				"url: '/api/v1/openapi.yaml'",
				// No developer toolbar, AI agent or MCP promos on a public reference.
				"showDeveloperTools: 'never'",
				"agent: { disabled: true }",
			},
			wantHeaders: map[string]string{
				"X-Content-Type-Options": "nosniff",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			newOpenAPIEngine(spec).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			expectStatus(t, w, http.StatusOK)
			if got := w.Header().Get("Content-Type"); got != tt.wantType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantType)
			}
			for _, want := range tt.wantBody {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("body does not contain %q:\n%s", want, w.Body.String())
				}
			}
			for k, v := range tt.wantHeaders {
				if got := w.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
		})
	}

	t.Run("docs page has a CSP that allows only the CDN and same-origin calls", func(t *testing.T) {
		w := httptest.NewRecorder()
		newOpenAPIEngine(spec).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
		csp := w.Header().Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "connect-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("CSP %q lacks %q", csp, want)
			}
		}
	})

	t.Run("the docs page is not under /api/v1", func(t *testing.T) {
		w := httptest.NewRecorder()
		newOpenAPIEngine(spec).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/docs", nil))
		expectStatus(t, w, http.StatusNotFound)
	})
}
