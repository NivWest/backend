package middleware

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

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

func assertJSONError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, wantStatus, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == nil {
		t.Errorf("expected {\"error\": ...} body, got %s", w.Body.String())
	}
}

func TestRequireAuth(t *testing.T) {
	auth := testutil.NewAuthRepo().WithSession("valid", &models.User{ID: 42, Role: "member"})
	r := newRouter(RequireAuth(auth))

	t.Run("missing cookie returns 401", func(t *testing.T) {
		assertJSONError(t, doGet(r, ""), http.StatusUnauthorized)
	})
	t.Run("unknown session returns 401", func(t *testing.T) {
		assertJSONError(t, doGet(r, "expired"), http.StatusUnauthorized)
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
		assertJSONError(t, doGet(newRouter(RequireRole("admin")), ""), http.StatusForbidden)
	})

	r := newRouter(RequireAuth(auth), RequireRole("admin"))
	t.Run("wrong role returns 403", func(t *testing.T) {
		assertJSONError(t, doGet(r, "member"), http.StatusForbidden)
	})
	t.Run("matching role passes", func(t *testing.T) {
		if w := doGet(r, "admin"); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})
}

func TestRateLimit_BlocksAfterBurstPerIP(t *testing.T) {
	r := gin.New()
	r.GET("/", RateLimit(), func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func(ip string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":12345"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// Burst is 10; the 11th immediate request from the same IP must be rejected.
	for i := 0; i < 10; i++ {
		if code := call("203.0.113.10"); code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, code)
		}
	}
	if code := call("203.0.113.10"); code != http.StatusTooManyRequests {
		t.Fatalf("request 11: status = %d, want 429", code)
	}
	// A different IP has its own bucket.
	if code := call("203.0.113.11"); code != http.StatusOK {
		t.Fatalf("other IP: status = %d, want 200", code)
	}
}

