package observability

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRedactValue(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		forbidden []string
	}{
		{
			name:      "session_id cookie in string",
			input:     "request cookie: session_id=secret-guest-token-12345; Path=/",
			forbidden: []string{"secret-guest-token-12345"},
		},
		{
			name:      "cookie header line",
			input:     "Cookie: session_id=my-secret-token",
			forbidden: []string{"my-secret-token"},
		},
		{
			name:      "authorization bearer header",
			input:     "Authorization: Bearer my-secret-jwt-token-999",
			forbidden: []string{"my-secret-jwt-token-999"},
		},
		{
			name:      "bearer in text",
			input:     "auth failed with Bearer abc.def.ghi",
			forbidden: []string{"abc.def.ghi"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactValue(tc.input)
			for _, f := range tc.forbidden {
				if strings.Contains(got, f) {
					t.Errorf("RedactValue(%q) = %q, still contains forbidden value %q", tc.input, got, f)
				}
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("RedactValue(%q) = %q, expected [REDACTED]", tc.input, got)
			}
		})
	}
}

func TestLogger_RedactionInLogOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "info", "json")

	logger.Info("user request with session",
		"cookie", "session_id=super-secret-session-abc",
		"authorization", "Bearer super-secret-bearer-xyz",
		"note", "client sent session_id=in-text-token-456",
	)

	out := buf.String()
	for _, forbidden := range []string{
		"super-secret-session-abc",
		"super-secret-bearer-xyz",
		"in-text-token-456",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("log output contains forbidden secret %q: %s", forbidden, out)
		}
	}
}

func TestRequestLogger_RequestIDAndJSONKeys(t *testing.T) {
	t.Run("propagates existing X-Request-ID and outputs expected JSON keys", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(&buf, "info", "json")

		engine := gin.New()
		engine.Use(RequestLogger(logger))
		engine.GET("/api/v1/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
		req.Header.Set("X-Request-ID", "test-abc-123")
		engine.ServeHTTP(w, req)

		if got := w.Header().Get("X-Request-ID"); got != "test-abc-123" {
			t.Errorf("X-Request-ID header = %q, want test-abc-123", got)
		}

		var logEntry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
			t.Fatalf("failed to parse log entry as JSON: %v, raw: %s", err, buf.String())
		}

		requiredKeys := []string{"time", "level", "msg", "request_id", "method", "route", "status", "duration_ms"}
		for _, k := range requiredKeys {
			if _, ok := logEntry[k]; !ok {
				t.Errorf("log entry missing required key %q, entry: %+v", k, logEntry)
			}
		}

		if logEntry["request_id"] != "test-abc-123" {
			t.Errorf("logEntry[request_id] = %v, want test-abc-123", logEntry["request_id"])
		}
		if logEntry["route"] != "/api/v1/test" {
			t.Errorf("logEntry[route] = %v, want /api/v1/test", logEntry["route"])
		}
		if logEntry["method"] != "GET" {
			t.Errorf("logEntry[method] = %v, want GET", logEntry["method"])
		}
	})

	t.Run("generates new X-Request-ID when not provided", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(&buf, "info", "json")

		engine := gin.New()
		engine.Use(RequestLogger(logger))
		engine.GET("/api/v1/ping", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ping", nil)
		engine.ServeHTTP(w, req)

		reqID := w.Header().Get("X-Request-ID")
		if reqID == "" {
			t.Fatal("expected X-Request-ID header to be generated and set on response")
		}

		var logEntry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
			t.Fatalf("failed to parse log entry as JSON: %v", err)
		}

		if logEntry["request_id"] != reqID {
			t.Errorf("logEntry[request_id] = %v, want %s", logEntry["request_id"], reqID)
		}
	})
}
