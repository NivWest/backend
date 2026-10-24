package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
)

const (
	memberSession = "member-session"
	adminSession  = "admin-session"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// newAuth returns an AuthRepo with one member (ID 1) and one admin (ID 2) session.
func newAuth() *testutil.AuthRepo {
	return testutil.NewAuthRepo().
		WithSession(memberSession, &models.User{ID: 1, Role: "member", Email: "member@example.com"}).
		WithSession(adminSession, &models.User{ID: 2, Role: "admin", Email: "admin@example.com"})
}

// newEngine mounts the given registrars under /api/v1, mirroring cmd/api.
func newEngine(registrars ...interface{ Register(*gin.RouterGroup) }) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/api/v1")
	for _, reg := range registrars {
		reg.Register(v1)
	}
	return r
}

func do(t *testing.T, r http.Handler, method, path, session string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			reader = bytes.NewBufferString(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			reader = bytes.NewReader(raw)
		}
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "session_id", Value: session})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func expectStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status = %d, want %d (body %s)", w.Code, want, w.Body.String())
	}
}

func expectErrorBody(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == nil {
		t.Errorf("expected {\"error\": ...} body, got %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Orders
// ---------------------------------------------------------------------------

func TestOrderHandler_PlaceOrder(t *testing.T) {
	quote := testutil.FixedQuote(domain.Quote{Last: 100})

	t.Run("requires authentication", func(t *testing.T) {
		svc := services.NewOrderService(&testutil.OrderRepo{}, &testutil.MarketProvider{QuoteFn: quote})
		w := do(t, newEngine(NewOrderHandler(svc, newAuth())), http.MethodPost, "/api/v1/orders", "", map[string]any{})
		expectStatus(t, w, http.StatusUnauthorized)
	})

	invalid := map[string]any{
		"missing symbol":    map[string]any{"type": "BUY", "quantity": 1},
		"invalid type":      map[string]any{"symbol": "X", "type": "HOLD", "quantity": 1},
		"zero quantity":     map[string]any{"symbol": "X", "type": "BUY", "quantity": 0},
		"negative quantity": map[string]any{"symbol": "X", "type": "SELL", "quantity": -1},
		"malformed json":    "{",
	}
	for name, body := range invalid {
		t.Run("rejects "+name, func(t *testing.T) {
			repo := &testutil.OrderRepo{}
			svc := services.NewOrderService(repo, &testutil.MarketProvider{QuoteFn: quote})
			w := do(t, newEngine(NewOrderHandler(svc, newAuth())), http.MethodPost, "/api/v1/orders", memberSession, body)
			expectStatus(t, w, http.StatusBadRequest)
			expectErrorBody(t, w)
			if len(repo.Executed) != 0 {
				t.Error("no transaction may be executed for an invalid request")
			}
		})
	}

	t.Run("executes valid order for current user", func(t *testing.T) {
		repo := &testutil.OrderRepo{}
		svc := services.NewOrderService(repo, &testutil.MarketProvider{QuoteFn: quote})
		w := do(t, newEngine(NewOrderHandler(svc, newAuth())), http.MethodPost, "/api/v1/orders", memberSession,
			domain.OrderRequest{Symbol: "VOLV-B", Type: "BUY", Quantity: 2})
		expectStatus(t, w, http.StatusOK)
		if len(repo.Executed) != 1 || repo.Executed[0].UserID != 1 || repo.Executed[0].Total != 200 {
			t.Errorf("unexpected executed orders: %+v", repo.Executed)
		}
	})

	t.Run("business rule violation returns 400", func(t *testing.T) {
		repo := &testutil.OrderRepo{ExecuteTransactionFn: func(context.Context, testutil.ExecutedOrder) error {
			return errors.New("insufficient funds")
		}}
		svc := services.NewOrderService(repo, &testutil.MarketProvider{QuoteFn: quote})
		w := do(t, newEngine(NewOrderHandler(svc, newAuth())), http.MethodPost, "/api/v1/orders", memberSession,
			domain.OrderRequest{Symbol: "VOLV-B", Type: "BUY", Quantity: 1})
		expectStatus(t, w, http.StatusBadRequest)
		expectErrorBody(t, w)
	})
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func expectErrorCode(t *testing.T, w *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var body domain.ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error == "" || body.Code != wantCode {
		t.Errorf("expected {\"error\": ..., \"code\": %q} body, got %s", wantCode, w.Body.String())
	}
}

func TestUserHandler(t *testing.T) {
	const guestSession = "guest-session"
	newRouterWithAuth := func(users *testutil.UserRepo, auth *testutil.AuthRepo) *gin.Engine {
		return newEngine(NewUserHandler(services.NewUserService(users), auth))
	}
	newRouter := func(users *testutil.UserRepo) *gin.Engine {
		return newRouterWithAuth(users, newAuth())
	}

	t.Run("legacy GET /users/:id is gone", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1, Email: "one@example.com"})
		w := do(t, newRouter(users), http.MethodGet, "/api/v1/users/1", "", nil)
		expectStatus(t, w, http.StatusNotFound)
	})

	t.Run("legacy POST /users/ is gone", func(t *testing.T) {
		users := testutil.NewUserRepo()
		w := do(t, newRouter(users), http.MethodPost, "/api/v1/users/", "", map[string]any{"email": "new@example.com"})
		expectStatus(t, w, http.StatusNotFound)
		if len(users.Users) != 0 {
			t.Errorf("no user may be created, got %d", len(users.Users))
		}
	})

	t.Run("GET /user/profile requires auth", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodGet, "/api/v1/user/profile", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	})

	t.Run("GET /user/profile returns public profile without google_id", func(t *testing.T) {
		auth := testutil.NewAuthRepo().WithSession(memberSession, &models.User{
			ID: 1, Name: "Member", Email: "member@example.com", Role: domain.RoleMember,
			Balance: 2500, GoogleID: "google-secret-123",
		})
		w := do(t, newRouterWithAuth(testutil.NewUserRepo(), auth), http.MethodGet, "/api/v1/user/profile", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode profile: %v (body %s)", err, w.Body.String())
		}
		want := map[string]any{
			"id": float64(1), "name": "Member", "email": "member@example.com",
			"role": domain.RoleMember, "balance": float64(2500), "is_guest": false,
		}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("profile[%q] = %v, want %v", k, body[k], v)
			}
		}
		if _, ok := body["google_id"]; ok {
			t.Errorf("profile must not contain google_id: %s", w.Body.String())
		}
		if len(body) != len(want) {
			t.Errorf("profile has unexpected keys: %s", w.Body.String())
		}
	})

	t.Run("GET /user/profile marks guests", func(t *testing.T) {
		auth := newAuth().WithSession(guestSession, &models.User{
			ID: 3, Name: domain.GuestName, Email: "guest-x@" + domain.GuestEmailDomain, Role: domain.RoleGuest,
			Balance: domain.GuestStartingBalance, GoogleID: domain.GuestGoogleIDPrefix + "x",
		})
		w := do(t, newRouterWithAuth(testutil.NewUserRepo(), auth), http.MethodGet, "/api/v1/user/profile", guestSession, nil)
		expectStatus(t, w, http.StatusOK)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode profile: %v", err)
		}
		if body["is_guest"] != true || body["role"] != domain.RoleGuest || body["id"] != float64(3) {
			t.Errorf("guest profile = %s", w.Body.String())
		}
		if _, ok := body["google_id"]; ok {
			t.Errorf("profile must not contain google_id: %s", w.Body.String())
		}
	})

	t.Run("DELETE /admin/users/:id requires auth", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo(&models.User{ID: 7})), http.MethodDelete, "/api/v1/admin/users/7", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
		expectErrorCode(t, w, domain.CodeUnauthenticated)
	})

	t.Run("DELETE /admin/users/:id forbids non-admin", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1})
		w := do(t, newRouter(users), http.MethodDelete, "/api/v1/admin/users/1", memberSession, nil)
		expectStatus(t, w, http.StatusForbidden)
		expectErrorCode(t, w, domain.CodeForbidden)
		if _, ok := users.Users[1]; !ok {
			t.Error("user must not be deleted by a non-admin")
		}
	})

	t.Run("DELETE /admin/users/:id maps not found to 404", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodDelete, "/api/v1/admin/users/99", adminSession, nil)
		expectStatus(t, w, http.StatusNotFound)
		expectErrorCode(t, w, domain.CodeUserNotFound)
	})

	for _, id := range []string{"-1", "0", "abc"} {
		t.Run("DELETE /admin/users/:id rejects invalid id "+id, func(t *testing.T) {
			w := do(t, newRouter(testutil.NewUserRepo()), http.MethodDelete, "/api/v1/admin/users/"+id, adminSession, nil)
			expectStatus(t, w, http.StatusBadRequest)
			expectErrorCode(t, w, domain.CodeValidation)
		})
	}

	t.Run("DELETE /admin/users/:id maps repository failure to 500", func(t *testing.T) {
		users := testutil.NewUserRepo()
		users.DeleteFn = func(context.Context, uint) error { return errors.New("db down") }
		w := do(t, newRouter(users), http.MethodDelete, "/api/v1/admin/users/7", adminSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorCode(t, w, domain.CodeInternal)
	})

	t.Run("DELETE /admin/users/:id returns 204", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 7})
		w := do(t, newRouter(users), http.MethodDelete, "/api/v1/admin/users/7", adminSession, nil)
		expectStatus(t, w, http.StatusNoContent)
		if _, ok := users.Users[7]; ok {
			t.Error("user 7 should be deleted")
		}
	})
}

// ---------------------------------------------------------------------------
// Watchlists
// ---------------------------------------------------------------------------

func TestWatchlistHandler(t *testing.T) {
	newRouter := func(repo *testutil.WatchlistRepo) *gin.Engine {
		return newEngine(NewWatchlistHandler(services.NewWatchlistService(repo), newAuth()))
	}

	t.Run("all routes require auth", func(t *testing.T) {
		w := do(t, newRouter(&testutil.WatchlistRepo{}), http.MethodGet, "/api/v1/watchlists", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("create requires name", func(t *testing.T) {
		w := do(t, newRouter(&testutil.WatchlistRepo{}), http.MethodPost, "/api/v1/watchlists", memberSession, map[string]any{})
		expectStatus(t, w, http.StatusBadRequest)
	})

	t.Run("create scopes watchlist to current user", func(t *testing.T) {
		var gotUser uint
		repo := &testutil.WatchlistRepo{CreateFn: func(_ context.Context, userID uint, name string) (*models.Watchlist, error) {
			gotUser = userID
			return &models.Watchlist{ID: 10, UserID: userID, Name: name}, nil
		}}
		w := do(t, newRouter(repo), http.MethodPost, "/api/v1/watchlists", memberSession, domain.WatchlistRequest{Name: "Tech"})
		expectStatus(t, w, http.StatusCreated)
		if gotUser != 1 {
			t.Errorf("watchlist created for user %d, want 1", gotUser)
		}
	})

	t.Run("create failure returns 500", func(t *testing.T) {
		repo := &testutil.WatchlistRepo{CreateFn: func(context.Context, uint, string) (*models.Watchlist, error) {
			return nil, errors.New("db down")
		}}
		w := do(t, newRouter(repo), http.MethodPost, "/api/v1/watchlists", memberSession, domain.WatchlistRequest{Name: "Tech"})
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorBody(t, w)
	})

	t.Run("delete rejects invalid id", func(t *testing.T) {
		w := do(t, newRouter(&testutil.WatchlistRepo{}), http.MethodDelete, "/api/v1/watchlists/xyz", memberSession, nil)
		expectStatus(t, w, http.StatusBadRequest)
	})

	t.Run("add item returns 201", func(t *testing.T) {
		w := do(t, newRouter(&testutil.WatchlistRepo{}), http.MethodPost, "/api/v1/watchlists/3/items", memberSession,
			domain.WatchlistItemRequest{Symbol: "ABB"})
		expectStatus(t, w, http.StatusCreated)
	})

	t.Run("remove item returns 204", func(t *testing.T) {
		w := do(t, newRouter(&testutil.WatchlistRepo{}), http.MethodDelete, "/api/v1/watchlists/3/items/ABB", memberSession, nil)
		expectStatus(t, w, http.StatusNoContent)
	})
}

// ---------------------------------------------------------------------------
// Portfolio
// ---------------------------------------------------------------------------

func TestPortfolioHandler_Dashboard(t *testing.T) {
	t.Run("requires auth", func(t *testing.T) {
		svc := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.MarketProvider{}, testutil.NewUserRepo())
		w := do(t, newEngine(NewPortfolioHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/dashboard", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		svc := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.MarketProvider{}, testutil.NewUserRepo())
		w := do(t, newEngine(NewPortfolioHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/dashboard", memberSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorBody(t, w)
	})

	t.Run("returns dashboard", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1, Balance: 1234})
		svc := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.MarketProvider{}, users)
		w := do(t, newEngine(NewPortfolioHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/dashboard", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		var dash services.PortfolioDashboard
		_ = json.Unmarshal(w.Body.Bytes(), &dash)
		if dash.Balance != 1234 {
			t.Errorf("balance = %v, want 1234", dash.Balance)
		}
	})
}
