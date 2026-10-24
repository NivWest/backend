package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	log.SetOutput(io.Discard) // middleware logs every request
	os.Exit(m.Run())
}

// newRouter mounts the given middleware in front of a handler that echoes the current user.
func newRouter(mw ...gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	handlers := append(mw, func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"user": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user_id": user.ID})
	})
	r.GET("/", handlers...)
	return r
}

func doGet(r http.Handler, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "session_id", Value: cookie})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// assertJSONError checks the status and that the body is a domain.ErrorResponse with the given code.
func assertJSONError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, wantStatus, w.Body.String())
	}
	var body domain.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not an ErrorResponse: %v (body %s)", err, w.Body.String())
	}
	if body.Error == "" {
		t.Errorf("error message is empty (body %s)", w.Body.String())
	}
	if body.Code != wantCode {
		t.Errorf("code = %q, want %q", body.Code, wantCode)
	}
}

func TestRequireAuth(t *testing.T) {
	auth := testutil.NewAuthRepo().WithSession("valid", &models.User{ID: 42, Role: "member"})
	r := newRouter(RequireAuth(auth))

	t.Run("missing cookie returns 401", func(t *testing.T) {
		assertJSONError(t, doGet(r, ""), http.StatusUnauthorized, domain.CodeUnauthenticated)
	})
	t.Run("unknown session returns 401", func(t *testing.T) {
		assertJSONError(t, doGet(r, "expired"), http.StatusUnauthorized, domain.CodeUnauthenticated)
	})
	t.Run("valid session stores user in context", func(t *testing.T) {
		w := doGet(r, "valid")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var body struct {
			UserID uint `json:"user_id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.UserID != 42 {
			t.Errorf("user_id = %d, want 42", body.UserID)
		}
	})
}

func TestRequireRole(t *testing.T) {
	auth := testutil.NewAuthRepo().
		WithSession("admin", &models.User{ID: 1, Role: "admin"}).
		WithSession("member", &models.User{ID: 2, Role: "member"})

	t.Run("without authenticated user returns 403", func(t *testing.T) {
		assertJSONError(t, doGet(newRouter(RequireRole("admin")), ""), http.StatusForbidden, domain.CodeForbidden)
	})

	r := newRouter(RequireAuth(auth), RequireRole("admin"))
	t.Run("wrong role returns 403", func(t *testing.T) {
		assertJSONError(t, doGet(r, "member"), http.StatusForbidden, domain.CodeForbidden)
	})
	t.Run("matching role passes", func(t *testing.T) {
		if w := doGet(r, "admin"); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})
}

// newIPCaller returns a function that sends one GET from the given IP through mw and returns the recorder.
func newIPCaller(mw gin.HandlerFunc) func(ip string) *httptest.ResponseRecorder {
	r := gin.New()
	r.GET("/", mw, func(c *gin.Context) { c.Status(http.StatusOK) })
	return func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
}

func TestRateLimit_BlocksAfterBurstPerIP(t *testing.T) {
	call := newIPCaller(RateLimit())

	// Burst is 10; the 11th immediate request from the same IP must be rejected.
	for i := 0; i < 10; i++ {
		if w := call("203.0.113.10"); w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, w.Code)
		}
	}
	assertJSONError(t, call("203.0.113.10"), http.StatusTooManyRequests, domain.CodeRateLimited)
	// A different IP has its own bucket.
	if w := call("203.0.113.11"); w.Code != http.StatusOK {
		t.Fatalf("other IP: status = %d, want 200", w.Code)
	}
}

func TestRateLimitPerMinute_AllowsLimitThenBlocksPerIP(t *testing.T) {
	call := newIPCaller(RateLimitPerMinute(10))

	for i := 0; i < 10; i++ {
		if w := call("198.51.100.1"); w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, w.Code)
		}
	}
	assertJSONError(t, call("198.51.100.1"), http.StatusTooManyRequests, domain.CodeRateLimited)

	// A second IP is unaffected.
	for i := 0; i < 10; i++ {
		if w := call("198.51.100.2"); w.Code != http.StatusOK {
			t.Fatalf("second IP request %d: status = %d, want 200", i+1, w.Code)
		}
	}
}

func TestRateLimitPerMinute_EachCallOwnsItsBuckets(t *testing.T) {
	first := newIPCaller(RateLimitPerMinute(1))
	second := newIPCaller(RateLimitPerMinute(1))

	if w := first("198.51.100.3"); w.Code != http.StatusOK {
		t.Fatalf("first limiter: status = %d, want 200", w.Code)
	}
	assertJSONError(t, first("198.51.100.3"), http.StatusTooManyRequests, domain.CodeRateLimited)
	if w := second("198.51.100.3"); w.Code != http.StatusOK {
		t.Fatalf("second limiter shares state with the first: status = %d, want 200", w.Code)
	}
}

func TestRequireAuth_LogsNeverContainSessionIDOrEmail(t *testing.T) {
	const (
		validSession   = "sess-valid-7f3a9c2e"
		invalidSession = "sess-invalid-b81d44f0"
		email          = "trader@example.com"
	)
	auth := testutil.NewAuthRepo().
		WithSession(validSession, &models.User{ID: 7, Email: email, Role: "admin"})

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	authOnly := newRouter(RequireAuth(auth))
	withRole := newRouter(RequireAuth(auth), RequireRole("member"))

	if w := doGet(authOnly, validSession); w.Code != http.StatusOK {
		t.Fatalf("valid session: status = %d, want 200", w.Code)
	}
	assertJSONError(t, doGet(authOnly, invalidSession), http.StatusUnauthorized, domain.CodeUnauthenticated)
	assertJSONError(t, doGet(withRole, validSession), http.StatusForbidden, domain.CodeForbidden)

	out := buf.String()
	if out == "" {
		t.Fatal("expected the rejected requests to be logged; log capture is not working")
	}
	for _, secret := range []string{validSession, invalidSession, email} {
		if strings.Contains(out, secret) {
			t.Errorf("log output contains %q:\n%s", secret, out)
		}
	}
}
