package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// OpenAPIContentType is the media type GET /api/v1/openapi.yaml answers with (RFC 9512).
const OpenAPIContentType = "application/yaml"

// Scalar API reference, pinned to one published build. Bump both together: the SRI hash is the sha384 of
// https://cdn.jsdelivr.net/npm/@scalar/api-reference@<version>/dist/browser/standalone.js.
const (
	scalarVersion = "1.76.0"
	scalarSRI     = "sha384-oH1C0RwqcwbVXkC+r8A+4qRoyEUQdtxAzOwumkxZtQBBcsy61FOoPt7B50OVJYYQ"
	scalarScript  = "https://cdn.jsdelivr.net/npm/@scalar/api-reference@" + scalarVersion + "/dist/browser/standalone.js"
)

// docsPage renders the served spec with Scalar. The spec URL is relative, so the page works on the API
// origin and behind the frontend's /api/* rewrites alike.
const docsPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>TradeEdu API reference</title>
</head>
<body>
  <noscript>This interactive reference needs JavaScript. The raw contract is at <a href="/api/v1/openapi.yaml">/api/v1/openapi.yaml</a>.</noscript>
  <div id="api-reference"></div>
  <script src="` + scalarScript + `" integrity="` + scalarSRI + `" crossorigin="anonymous"></script>
  <script>
    Scalar.createApiReference('#api-reference', {
      url: '/api/v1/openapi.yaml',
      theme: 'default',
      darkMode: true,
      hideClientButton: true,
      showDeveloperTools: 'never',
      agent: { disabled: true },
      mcp: { disabled: true },
      telemetry: false,
    })
  </script>
</body>
</html>
`

// docsCSP limits the docs page to its inline bootstrap, the pinned CDN script and same-origin requests
// (the spec, and "try it" calls against this API).
const docsCSP = "default-src 'none'; " +
	"script-src 'unsafe-inline' https://cdn.jsdelivr.net; " +
	"style-src 'unsafe-inline' https://cdn.jsdelivr.net https://fonts.scalar.com; " +
	"font-src https://cdn.jsdelivr.net https://fonts.scalar.com data:; " +
	"img-src 'self' data: https:; " +
	"connect-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// OpenAPIHandler serves the embedded OpenAPI contract and an interactive reference for it (story NW-07).
// Both routes are public: the contract describes no secrets.
type OpenAPIHandler struct {
	spec []byte
}

// NewOpenAPIHandler takes the contract to serve (api.OpenAPISpec in production).
func NewOpenAPIHandler(spec []byte) *OpenAPIHandler {
	return &OpenAPIHandler{spec: spec}
}

// Register mounts GET /api/v1/openapi.yaml.
func (h *OpenAPIHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/openapi.yaml", h.Spec)
}

// RegisterDocs mounts GET /docs on the /api group, i.e. GET /api/docs. It lives outside /api/v1 because it
// is a page about the API, not part of it.
func (h *OpenAPIHandler) RegisterDocs(api *gin.RouterGroup) {
	api.GET("/docs", h.Docs)
}

func (h *OpenAPIHandler) Spec(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, OpenAPIContentType, h.spec)
}

func (h *OpenAPIHandler) Docs(c *gin.Context) {
	c.Header("Content-Security-Policy", docsCSP)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(docsPage))
}
