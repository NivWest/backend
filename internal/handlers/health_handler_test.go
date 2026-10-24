package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"backend/internal/domain"
	"backend/internal/version"
)

func TestHealthReturnsStatusVersionFeaturesAndProvider(t *testing.T) {
	cases := []struct {
		name     string
		features domain.Features
		provider string
	}{
		{"avanza, guest on", domain.Features{GuestAuth: true}, "avanza"},
		{"avanza, guest off", domain.Features{}, "avanza"},
		{"simulated, sim control on", domain.Features{GuestAuth: true, SimControl: true}, "simulated"},
		{"simulated, sim control off", domain.Features{GuestAuth: false, SimControl: false}, "simulated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newEngine(NewHealthHandler(tc.features, tc.provider))

			w := do(t, r, http.MethodGet, "/api/v1/health", "", nil)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
			}
			var body domain.HealthResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Status != "ok" || body.Version != version.Version {
				t.Fatalf("body = %+v, want status ok and version %q", body, version.Version)
			}
			if body.Features != tc.features {
				t.Fatalf("features = %+v, want %+v", body.Features, tc.features)
			}
			if body.MarketDataProvider != tc.provider {
				t.Fatalf("market_data_provider = %q, want %q", body.MarketDataProvider, tc.provider)
			}

			// The wire names must match api/openapi.yaml#/components/schemas/Health.
			var raw struct {
				Features           map[string]json.RawMessage `json:"features"`
				MarketDataProvider *string                    `json:"market_data_provider"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode raw body: %v", err)
			}
			if raw.MarketDataProvider == nil || *raw.MarketDataProvider != tc.provider {
				t.Fatalf("body %s: market_data_provider missing or wrong, want %q", w.Body.String(), tc.provider)
			}
			for key, want := range map[string]bool{"guest_auth": tc.features.GuestAuth, "sim_control": tc.features.SimControl} {
				v, ok := raw.Features[key]
				if !ok {
					t.Fatalf("body %s: features has no %s field", w.Body.String(), key)
				}
				var got bool
				if err := json.Unmarshal(v, &got); err != nil || got != want {
					t.Fatalf("features.%s = %s, want %v", key, v, want)
				}
			}
		})
	}
}
