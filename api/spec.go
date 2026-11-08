// Package api embeds the OpenAPI contract (openapi.yaml), the cross-repo source of truth for the HTTP API.
// The handlers serve it at GET /api/v1/openapi.yaml and render it at GET /api/docs (story NW-07).
package api

import _ "embed"

// OpenAPISpec is api/openapi.yaml as it was when the binary was built.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
