package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type contextKey string

const (
	RequestIDHeader                = "X-Request-ID"
	RequestIDContextKey contextKey = "observability_request_id"
	LoggerContextKey    contextKey = "observability_logger"
)

var (
	// Regexes to redact sensitive values embedded in log messages or headers
	sessionIDRegex = regexp.MustCompile(`(?i)(session_id\s*=\s*)([^;\s]+)`)
	bearerRegex    = regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9_\-\.]+)`)
	authHdrRegex   = regexp.MustCompile(`(?i)(authorization\s*:\s*)([^\r\n]+)`)
	cookieHdrRegex = regexp.MustCompile(`(?i)(cookie\s*:\s*)([^\r\n]+)`)
)

// RedactValue strips credentials, session cookies, and Authorization tokens from s.
func RedactValue(s string) string {
	if s == "" {
		return s
	}
	s = sessionIDRegex.ReplaceAllString(s, `${1}[REDACTED]`)
	s = bearerRegex.ReplaceAllString(s, `${1}[REDACTED]`)
	s = authHdrRegex.ReplaceAllString(s, `${1}[REDACTED]`)
	s = cookieHdrRegex.ReplaceAllString(s, `${1}[REDACTED]`)
	return s
}

// RedactAttr inspects slog attributes and redacts sensitive keys or embedded values.
func RedactAttr(groups []string, a slog.Attr) slog.Attr {
	key := strings.ToLower(a.Key)
	switch key {
	case "cookie", "set-cookie", "authorization", "session_id", "password", "token", "secret", "api_key", "anthropic_api_key":
		return slog.String(a.Key, "[REDACTED]")
	}

	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, RedactValue(a.Value.String()))
	}
	return a
}

// ParseLevel translates level names ("debug", "info", "warn", "error") to slog.Level.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// NewLogger constructs an slog.Logger writing JSON or text to w with redaction enabled.
func NewLogger(w io.Writer, levelStr, formatStr string) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	lvl := ParseLevel(levelStr)
	opts := &slog.HandlerOptions{
		Level:       lvl,
		ReplaceAttr: RedactAttr,
	}

	var handler slog.Handler
	if strings.ToLower(strings.TrimSpace(formatStr)) == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler)
}

// FromContext extracts the logger attached to ctx, or returns slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(LoggerContextKey).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}

// WithLogger returns a new context with logger attached.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, LoggerContextKey, logger)
}

// RequestIDFromContext returns the request ID from ctx, or an empty string.
func RequestIDFromContext(ctx context.Context) string {
	if ctx != nil {
		if id, ok := ctx.Value(RequestIDContextKey).(string); ok {
			return id
		}
	}
	return ""
}

// RequestLogger creates a Gin middleware that:
//  1. Reads or generates X-Request-ID and echoes it on the response (AC1).
//  2. Attaches the request_id and contextual logger to the request context.
//  3. Emits one structured JSON access log line after the request completes (AC4):
//     `time, level, msg, request_id, method, route, status, duration_ms`.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := strings.TrimSpace(c.GetHeader(RequestIDHeader))
		if reqID == "" {
			reqID = uuid.NewString()
		}
		c.Header(RequestIDHeader, reqID)

		reqLogger := logger.With(slog.String("request_id", reqID))
		ctx := context.WithValue(c.Request.Context(), RequestIDContextKey, reqID)
		ctx = context.WithValue(ctx, LoggerContextKey, reqLogger)
		c.Request = c.Request.WithContext(ctx)

		t0 := time.Now()
		c.Next()
		durationMs := time.Since(t0).Milliseconds()

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}

		attrs := []slog.Attr{
			slog.String("request_id", reqID),
			slog.String("method", c.Request.Method),
			slog.String("route", route),
			slog.Int("status", c.Writer.Status()),
			slog.Int64("duration_ms", durationMs),
		}

		if val, exists := c.Get("authenticated_user"); exists {
			if u, ok := val.(*models.User); ok && u != nil {
				attrs = append(attrs, slog.Int64("user_id", int64(u.ID)))
			}
		}

		reqLogger.LogAttrs(c.Request.Context(), slog.LevelInfo, "request", attrs...)
	}
}
