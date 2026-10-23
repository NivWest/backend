package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"backend/internal/version"
)

func TestHealthReturnsStatusAndVersion(t *testing.T) {
	r := newEngine(NewHealthHandler())

	w := do(t, r, http.MethodGet, "/api/v1/health", "", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" || body["version"] != version.Version {
		t.Fatalf("body = %v, want status ok and version %q", body, version.Version)
	}
}
