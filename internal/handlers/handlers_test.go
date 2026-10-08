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
	quote := testutil.QuoteJSON(`{"last":100}`)

	t.Run("requires authentication", func(t *testing.T) {
		svc := services.NewOrderService(&testutil.OrderRepo{}, &testutil.StockProvider{GetStockQuoteFn: quote})
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
			svc := services.NewOrderService(repo, &testutil.StockProvider{GetStockQuoteFn: quote})
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
		svc := services.NewOrderService(repo, &testutil.StockProvider{GetStockQuoteFn: quote})
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
		svc := services.NewOrderService(repo, &testutil.StockProvider{GetStockQuoteFn: quote})
		w := do(t, newEngine(NewOrderHandler(svc, newAuth())), http.MethodPost, "/api/v1/orders", memberSession,
			domain.OrderRequest{Symbol: "VOLV-B", Type: "BUY", Quantity: 1})
		expectStatus(t, w, http.StatusBadRequest)
		expectErrorBody(t, w)
	})
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func TestUserHandler(t *testing.T) {
	newRouter := func(users *testutil.UserRepo) *gin.Engine {
		return newEngine(NewUserHandler(services.NewUserService(users), newAuth()))
	}

	t.Run("GET /users/:id rejects non-numeric id", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodGet, "/api/v1/users/abc", "", nil)
		expectStatus(t, w, http.StatusBadRequest)
		expectErrorBody(t, w)
	})

	t.Run("GET /users/:id returns user", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 5, Email: "five@example.com"})
		w := do(t, newRouter(users), http.MethodGet, "/api/v1/users/5", "", nil)
		expectStatus(t, w, http.StatusOK)
	})

	t.Run("POST /users duplicate returns 409", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1, Email: "dup@example.com"})
		w := do(t, newRouter(users), http.MethodPost, "/api/v1/users/", "", map[string]any{"email": "dup@example.com"})
		expectStatus(t, w, http.StatusConflict)
		expectErrorBody(t, w)
	})

	t.Run("GET /user/profile requires auth", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodGet, "/api/v1/user/profile", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("GET /user/profile returns current user", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodGet, "/api/v1/user/profile", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		var u models.User
		_ = json.Unmarshal(w.Body.Bytes(), &u)
		if u.ID != 1 {
			t.Errorf("profile id = %d, want 1", u.ID)
		}
	})

	t.Run("DELETE /admin/users/:id forbids non-admin", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodDelete, "/api/v1/admin/users/1", memberSession, nil)
		expectStatus(t, w, http.StatusForbidden)
	})

	t.Run("DELETE /admin/users/:id maps not found to 404", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodDelete, "/api/v1/admin/users/99", adminSession, nil)
		expectStatus(t, w, http.StatusNotFound)
		expectErrorBody(t, w)
	})

	t.Run("DELETE /admin/users/:id rejects invalid id", func(t *testing.T) {
		w := do(t, newRouter(testutil.NewUserRepo()), http.MethodDelete, "/api/v1/admin/users/-1", adminSession, nil)
		expectStatus(t, w, http.StatusBadRequest)
	})

	t.Run("DELETE /admin/users/:id returns 204", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 7})
		w := do(t, newRouter(users), http.MethodDelete, "/api/v1/admin/users/7", adminSession, nil)
		expectStatus(t, w, http.StatusNoContent)
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
// Stocks & portfolio
// ---------------------------------------------------------------------------

func TestStockHandler(t *testing.T) {
	provider := &testutil.StockProvider{
		SearchFn: func(_ context.Context, q string) ([]byte, error) { return []byte(`{"q":"` + q + `"}`), nil },
	}
	r := newEngine(NewStockHandler(services.NewStockService(provider)))

	t.Run("search without query returns 400", func(t *testing.T) {
		w := do(t, r, http.MethodGet, "/api/v1/stocks/search", "", nil)
		expectStatus(t, w, http.StatusBadRequest)
		expectErrorBody(t, w)
	})

	t.Run("search passes upstream JSON through", func(t *testing.T) {
		w := do(t, r, http.MethodGet, "/api/v1/stocks/search?q=volvo", "", nil)
		expectStatus(t, w, http.StatusOK)
		if w.Body.String() != `{"q":"volvo"}` {
			t.Errorf("body = %s", w.Body.String())
		}
	})

	t.Run("quote without orderbookID returns 400", func(t *testing.T) {
		w := do(t, r, http.MethodGet, "/api/v1/stocks/quote", "", nil)
		expectStatus(t, w, http.StatusBadRequest)
	})
}

func TestPortfolioHandler_Dashboard(t *testing.T) {
	t.Run("requires auth", func(t *testing.T) {
		svc := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.StockProvider{}, testutil.NewUserRepo())
		w := do(t, newEngine(NewPortfolioHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/dashboard", "", nil)
		expectStatus(t, w, http.StatusUnauthorized)
	})

	t.Run("service error returns 500", func(t *testing.T) {
		svc := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.StockProvider{}, testutil.NewUserRepo())
		w := do(t, newEngine(NewPortfolioHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/dashboard", memberSession, nil)
		expectStatus(t, w, http.StatusInternalServerError)
		expectErrorBody(t, w)
	})

	t.Run("returns dashboard", func(t *testing.T) {
		users := testutil.NewUserRepo(&models.User{ID: 1, Balance: 1234})
		svc := services.NewPortfolioService(&testutil.PortfolioRepo{}, &testutil.StockProvider{}, users)
		w := do(t, newEngine(NewPortfolioHandler(svc, newAuth())), http.MethodGet, "/api/v1/portfolio/dashboard", memberSession, nil)
		expectStatus(t, w, http.StatusOK)
		var dash services.PortfolioDashboard
		_ = json.Unmarshal(w.Body.Bytes(), &dash)
		if dash.Balance != 1234 {
			t.Errorf("balance = %v, want 1234", dash.Balance)
		}
	})
}
