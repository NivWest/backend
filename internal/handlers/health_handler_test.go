package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"backend/internal/domain"
	"backend/internal/version"
)

func TestHealthReturnsStatusVersionAndFeatures(t *testing.T) {
	for _, guest := range []bool{true, false} {
		r := newEngine(NewHealthHandler(domain.Features{GuestAuth: guest}))

		w := do(t, r, http.MethodGet, "/api/v1/health", "", nil)

		if w.Code != http.StatusOK {
			t.Fatalf("guest_auth=%v: status = %d, want %d", guest, w.Code, http.StatusOK)
		}
		var body domain.HealthResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("guest_auth=%v: decode body: %v", guest, err)
		}
		if body.Status != "ok" || body.Version != version.Version {
			t.Fatalf("guest_auth=%v: body = %+v, want status ok and version %q", guest, body, version.Version)
		}
		if body.Features.GuestAuth != guest {
			t.Fatalf("features.guest_auth = %v, want %v", body.Features.GuestAuth, guest)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode raw body: %v", err)
		}
		if _, ok := raw["features"]; !ok {
			t.Fatalf("guest_auth=%v: body %s has no features field", guest, w.Body.String())
		}
	}
}
