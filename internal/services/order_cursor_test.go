package services

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"backend/internal/domain"
)

func TestOrderCursor_RoundTrip(t *testing.T) {
	stockholm := time.FixedZone("CEST", 2*60*60)
	tests := []struct {
		name string
		in   domain.OrderCursor
	}{
		{name: "whole seconds", in: domain.OrderCursor{CreatedAt: time.Date(2026, 10, 26, 9, 42, 0, 0, time.UTC), ID: 43}},
		{name: "microseconds (timestamptz)", in: domain.OrderCursor{CreatedAt: time.Date(2026, 10, 26, 9, 42, 0, 123456000, time.UTC), ID: 1}},
		{name: "nanoseconds", in: domain.OrderCursor{CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 987654321, time.UTC), ID: 7}},
		{name: "non-UTC zone", in: domain.OrderCursor{CreatedAt: time.Date(2026, 10, 26, 11, 42, 0, 500000000, stockholm), ID: 99}},
		{name: "large id", in: domain.OrderCursor{CreatedAt: time.Date(2026, 10, 26, 9, 42, 0, 0, time.UTC), ID: 1<<53 - 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := encodeOrderCursor(tt.in)
			if strings.ContainsAny(s, "+/=") {
				t.Errorf("cursor %q is not URL-safe unpadded base64", s)
			}
			got, err := decodeOrderCursor(s)
			if err != nil {
				t.Fatalf("decodeOrderCursor(%q) error = %v", s, err)
			}
			if !got.CreatedAt.Equal(tt.in.CreatedAt) || got.ID != tt.in.ID {
				t.Errorf("round trip = %+v, want %+v", got, tt.in)
			}
			if got.CreatedAt.Location() != time.UTC {
				t.Errorf("decoded time zone = %v, want UTC", got.CreatedAt.Location())
			}
		})
	}
}

func TestOrderCursor_MatchesDocumentedExample(t *testing.T) {
	// api/openapi.yaml GET /orders example: created_at 2026-10-26T09:42:00Z, id 43.
	const want = "eyJjIjoiMjAyNi0xMC0yNlQwOTo0MjowMFoiLCJpIjo0M30"
	got := encodeOrderCursor(domain.OrderCursor{CreatedAt: time.Date(2026, 10, 26, 9, 42, 0, 0, time.UTC), ID: 43})
	if got != want {
		t.Errorf("encodeOrderCursor() = %q, want %q", got, want)
	}
}

func TestOrderCursor_RejectsInvalid(t *testing.T) {
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	valid := encodeOrderCursor(domain.OrderCursor{CreatedAt: time.Date(2026, 10, 26, 9, 42, 0, 0, time.UTC), ID: 43})
	tests := map[string]string{
		"empty":                   "",
		"garbage":                 "not a cursor!",
		"padded std base64":       base64.StdEncoding.EncodeToString([]byte(`{"c":"2026-10-26T09:42:00Z","i":43}`)),
		"truncated":               valid[:len(valid)-5],
		"base64 of non-JSON":      b64("hello"),
		"JSON array":              b64(`[1,2]`),
		"empty object":            b64(`{}`),
		"missing time":            b64(`{"i":43}`),
		"missing id":              b64(`{"c":"2026-10-26T09:42:00Z"}`),
		"id 0":                    b64(`{"c":"2026-10-26T09:42:00Z","i":0}`),
		"negative id":             b64(`{"c":"2026-10-26T09:42:00Z","i":-1}`),
		"id as string":            b64(`{"c":"2026-10-26T09:42:00Z","i":"43"}`),
		"fractional id":           b64(`{"c":"2026-10-26T09:42:00Z","i":4.5}`),
		"time not RFC3339":        b64(`{"c":"26/10/2026","i":43}`),
		"time as number":          b64(`{"c":1761471720,"i":43}`),
		"zero time":               b64(`{"c":"0001-01-01T00:00:00Z","i":43}`),
		"overlong":                strings.Repeat("A", maxOrderCursorLength+1),
		"JSON null":               b64(`null`),
		"trailing garbage in b64": valid + "!!",
	}
	for name, cursor := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeOrderCursor(cursor)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("decodeOrderCursor(%q) error = %v, want *domain.ValidationError", cursor, err)
			}
			if verr.Msg != "cursor is invalid" {
				t.Errorf("Msg = %q, want %q", verr.Msg, "cursor is invalid")
			}
		})
	}
}
