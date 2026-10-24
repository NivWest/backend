package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
)

const guestTTL = 2 * time.Hour

func authConfig(guestEnabled bool, perMinute int) *config.Config {
	return &config.Config{Auth: config.Auth{
		FrontendURL:             "http://frontend.test",
		SessionTTL:              24 * time.Hour,
		OAuthStateTTL:           15 * time.Minute,
		GuestEnabled:            guestEnabled,
		GuestSessionTTL:         guestTTL,
		GuestRateLimitPerMinute: perMinute,
	}}
}

type authFixture struct {
	users  *testutil.UserRepo
	auth   *testutil.AuthRepo
	engine *gin.Engine
}

func newAuthFixture(cfg *config.Config) *authFixture {
	users := testutil.NewUserRepo()
	auth := testutil.NewAuthRepo()
	h := NewAuthHandler(services.NewAuthService(users, auth, cfg), cfg)
	return &authFixture{users: users, auth: auth, engine: newEngine(h)}
}

func sessionCookieOf(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in response (headers %v)", sessionCookie, w.Header())
	return nil
}

func decodeObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body %s)", err, w.Body.String())
	}
	return body
}

func TestAuthHandler_Guest(t *testing.T) {
	t.Run("creates a guest, returns its profile and sets the session cookie", func(t *testing.T) {
		f := newAuthFixture(authConfig(true, 10))
		w := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, w, http.StatusCreated)

		body := decodeObject(t, w)
		want := map[string]any{
			"role": domain.RoleGuest, "is_guest": true,
			"balance": domain.GuestStartingBalance, "name": domain.GuestName,
		}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("body[%q] = %v, want %v", k, body[k], v)
			}
		}
		if id, ok := body["id"].(float64); !ok || id == 0 {
			t.Errorf("body id = %v, want a non-zero id", body["id"])
		}
		if email, _ := body["email"].(string); !strings.HasSuffix(email, "@"+domain.GuestEmailDomain) {
			t.Errorf("body email = %q, want a guest address", email)
		}
		if _, ok := body["google_id"]; ok {
			t.Errorf("body must not contain google_id: %s", w.Body.String())
		}
		if len(body) != 6 {
			t.Errorf("body has unexpected keys: %s", w.Body.String())
		}

		c := sessionCookieOf(t, w)
		if c.Value == "" || !c.HttpOnly || c.Path != "/" {
			t.Errorf("session cookie = %+v, want non-empty HttpOnly cookie on /", c)
		}
		if c.MaxAge != int(guestTTL.Seconds()) {
			t.Errorf("cookie Max-Age = %d, want %d", c.MaxAge, int(guestTTL.Seconds()))
		}
		if c.Secure {
			t.Error("cookie must not be Secure when AUTH_COOKIE_SECURE is false")
		}
		if u, err := f.auth.GetSessionUser(t.Context(), c.Value); err != nil || u.ID != uint(body["id"].(float64)) {
			t.Errorf("session %q does not belong to the returned user (user %+v, err %v)", c.Value, u, err)
		}
	})

	t.Run("cookie is Secure when configured", func(t *testing.T) {
		cfg := authConfig(true, 10)
		cfg.Auth.CookieSecure = true
		w := do(t, newAuthFixture(cfg).engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, w, http.StatusCreated)
		if c := sessionCookieOf(t, w); !c.Secure || !c.HttpOnly {
			t.Errorf("session cookie = %+v, want Secure and HttpOnly", c)
		}
	})

	t.Run("two logins create two distinct guests and sessions", func(t *testing.T) {
		f := newAuthFixture(authConfig(true, 10))
		first := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		second := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, first, http.StatusCreated)
		expectStatus(t, second, http.StatusCreated)
		if a, b := decodeObject(t, first)["id"], decodeObject(t, second)["id"]; a == b {
			t.Errorf("both guests got id %v", a)
		}
		if a, b := sessionCookieOf(t, first).Value, sessionCookieOf(t, second).Value; a == b {
			t.Error("both guests got the same session id")
		}
		if len(f.users.Users) != 2 {
			t.Errorf("users created = %d, want 2", len(f.users.Users))
		}
	})

	t.Run("route is absent when guest login is disabled", func(t *testing.T) {
		f := newAuthFixture(authConfig(false, 10))
		w := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, w, http.StatusNotFound)
		if len(f.users.Users) != 0 {
			t.Error("no guest may be created when guest login is disabled")
		}
	})

	t.Run("rate limits per IP after the configured number of calls per minute", func(t *testing.T) {
		f := newAuthFixture(authConfig(true, 10))
		for i := 1; i <= 10; i++ {
			expectStatus(t, do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil), http.StatusCreated)
		}
		w := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, w, http.StatusTooManyRequests)
		expectErrorCode(t, w, domain.CodeRateLimited)
		if len(f.users.Users) != 10 {
			t.Errorf("users created = %d, want 10", len(f.users.Users))
		}
	})

	t.Run("user creation failure returns 500", func(t *testing.T) {
		f := newAuthFixture(authConfig(true, 10))
		f.users.CreateFn = func(_ context.Context, _ *models.User) error { return errors.New("db down") }
		w := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorCode(t, w, domain.CodeInternal)
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookie {
				t.Errorf("no session cookie may be set on failure, got %+v", c)
			}
		}
	})
}

func TestAuthHandler_Logout(t *testing.T) {
	t.Run("deletes the session and clears the cookie", func(t *testing.T) {
		f := newAuthFixture(authConfig(true, 10))
		login := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
		expectStatus(t, login, http.StatusCreated)
		session := sessionCookieOf(t, login).Value

		w := do(t, f.engine, http.MethodPost, "/api/v1/auth/logout", session, nil)
		expectStatus(t, w, http.StatusNoContent)
		if c := sessionCookieOf(t, w); c.Value != "" || c.MaxAge >= 0 {
			t.Errorf("logout cookie = %+v, want an expired empty cookie", c)
		}
		if _, err := f.auth.GetSessionUser(t.Context(), session); err == nil {
			t.Error("session must be deleted after logout")
		}
	})

	t.Run("without a session still returns 204", func(t *testing.T) {
		w := do(t, newAuthFixture(authConfig(true, 10)).engine, http.MethodPost, "/api/v1/auth/logout", "", nil)
		expectStatus(t, w, http.StatusNoContent)
	})
}

func TestAuthHandler_LogsNoSecrets(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	f := newAuthFixture(authConfig(true, 10))
	login := do(t, f.engine, http.MethodPost, "/api/v1/auth/guest", "", nil)
	expectStatus(t, login, http.StatusCreated)
	session := sessionCookieOf(t, login).Value
	email, _ := decodeObject(t, login)["email"].(string)
	expectStatus(t, do(t, f.engine, http.MethodPost, "/api/v1/auth/logout", session, nil), http.StatusNoContent)

	// A rejected OAuth callback must not echo either state value.
	const cookieState, queryState = "cookie-state-secret", "query-state-secret"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?state="+queryState+"&code=x", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: cookieState})
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	expectStatus(t, w, http.StatusBadRequest)

	out := buf.String()
	if out == "" {
		t.Fatal("expected auth log output")
	}
	for name, secret := range map[string]string{
		"session id": session, "email": email, "cookie state": cookieState, "query state": queryState,
	} {
		if secret == "" {
			t.Fatalf("%s is empty", name)
		}
		if strings.Contains(out, secret) {
			t.Errorf("log output contains the %s:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "user_id=") {
		t.Errorf("log output should identify the user by id:\n%s", out)
	}
}
