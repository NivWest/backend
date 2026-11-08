package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"backend/internal/domain"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
)

// nveMarket is SIM-NVE (Nordvik Energy) at 100; every other id is unknown.
func nveMarket() *testutil.MarketProvider {
	return &testutil.MarketProvider{
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			if id != "SIM-NVE" {
				return domain.InstrumentDetails{}, domain.ErrInstrumentNotFound
			}
			return domain.InstrumentDetails{OrderbookID: id, Symbol: "NVE", Name: "Nordvik Energy", LastPrice: 100, ChangePercent: -0.42}, nil
		},
		QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
			if id != "SIM-NVE" {
				return domain.Quote{}, domain.ErrInstrumentNotFound
			}
			return domain.Quote{OrderbookID: id, Symbol: "NVE", Last: 100, Bid: 99.9, Ask: 100.1, ChangePercent: -0.42}, nil
		},
	}
}

func watchlistEngine(repo *testutil.WatchlistRepo, provider *testutil.MarketProvider) *gin.Engine {
	return newEngine(NewWatchlistHandler(services.NewWatchlistService(repo, provider), newAuth()))
}

// seedEnergy gives the member (user 1) list 1 "Energy" holding SIM-NVE, and the admin (user 2) list 2 "Admin".
func seedEnergy(t *testing.T, repo *testutil.WatchlistRepo) {
	t.Helper()
	ctx := context.Background()
	svc := services.NewWatchlistService(repo, nveMarket())
	if _, err := svc.Create(ctx, 1, domain.WatchlistNameRequest{Name: "Energy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddItem(ctx, 1, 1, domain.WatchlistItemRequest{OrderbookID: "SIM-NVE"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, 2, domain.WatchlistNameRequest{Name: "Admin"}); err != nil {
		t.Fatal(err)
	}
}

func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not a JSON object: %s", raw)
	}
	return body
}

func TestWatchlistHandler_StatusCodes(t *testing.T) {
	long := strings.Repeat("x", 51)
	tests := []struct {
		name       string
		method     string
		path       string
		session    string
		body       any
		wantStatus int
		wantCode   string
	}{
		// Auth
		{name: "list needs a session", method: http.MethodGet, path: "/api/v1/watchlists", wantStatus: 401, wantCode: domain.CodeUnauthenticated},
		{name: "create needs a session", method: http.MethodPost, path: "/api/v1/watchlists", body: `{"name":"X"}`, wantStatus: 401, wantCode: domain.CodeUnauthenticated},
		{name: "remove needs a session", method: http.MethodDelete, path: "/api/v1/watchlists/1/items/SIM-NVE", wantStatus: 401, wantCode: domain.CodeUnauthenticated},

		// AC1 create
		{name: "create 201", method: http.MethodPost, path: "/api/v1/watchlists", session: memberSession, body: `{"name":"Tech"}`, wantStatus: 201},
		{name: "create blank name", method: http.MethodPost, path: "/api/v1/watchlists", session: memberSession, body: `{"name":"  "}`, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "create missing name", method: http.MethodPost, path: "/api/v1/watchlists", session: memberSession, body: `{}`, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "create 51 characters", method: http.MethodPost, path: "/api/v1/watchlists", session: memberSession, body: `{"name":"` + long + `"}`, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "create malformed JSON", method: http.MethodPost, path: "/api/v1/watchlists", session: memberSession, body: `{"name":`, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "create name not a string", method: http.MethodPost, path: "/api/v1/watchlists", session: memberSession, body: `{"name":5}`, wantStatus: 400, wantCode: domain.CodeValidation},

		// AC2 add item
		{name: "add 201", method: http.MethodPost, path: "/api/v1/watchlists/3/items", session: memberSession, body: `{"orderbook_id":"SIM-NVE"}`, wantStatus: 201},
		{name: "add duplicate 409", method: http.MethodPost, path: "/api/v1/watchlists/1/items", session: memberSession, body: `{"orderbook_id":"SIM-NVE"}`, wantStatus: 409, wantCode: domain.CodeConflict},
		{name: "add unknown instrument 404", method: http.MethodPost, path: "/api/v1/watchlists/1/items", session: memberSession, body: `{"orderbook_id":"SIM-NOPE"}`, wantStatus: 404, wantCode: domain.CodeInstrumentNotFound},
		{name: "add blank id 400", method: http.MethodPost, path: "/api/v1/watchlists/1/items", session: memberSession, body: `{"orderbook_id":""}`, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "add to another user's list 404", method: http.MethodPost, path: "/api/v1/watchlists/2/items", session: memberSession, body: `{"orderbook_id":"SIM-NVE"}`, wantStatus: 404, wantCode: domain.CodeWatchlistNotFound},

		// AC3 get
		{name: "get 200", method: http.MethodGet, path: "/api/v1/watchlists/1", session: memberSession, wantStatus: 200},
		{name: "get another user's list 404", method: http.MethodGet, path: "/api/v1/watchlists/1", session: adminSession, wantStatus: 404, wantCode: domain.CodeWatchlistNotFound},
		{name: "get missing list 404", method: http.MethodGet, path: "/api/v1/watchlists/99", session: memberSession, wantStatus: 404, wantCode: domain.CodeWatchlistNotFound},
		{name: "get non-numeric id 400", method: http.MethodGet, path: "/api/v1/watchlists/abc", session: memberSession, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "get id 0 400", method: http.MethodGet, path: "/api/v1/watchlists/0", session: memberSession, wantStatus: 400, wantCode: domain.CodeValidation},

		// AC4 rename / remove / delete, and the same on another user's list
		{name: "rename 200", method: http.MethodPatch, path: "/api/v1/watchlists/1", session: memberSession, body: `{"name":"Renewables"}`, wantStatus: 200},
		{name: "rename blank 400", method: http.MethodPatch, path: "/api/v1/watchlists/1", session: memberSession, body: `{"name":""}`, wantStatus: 400, wantCode: domain.CodeValidation},
		{name: "rename another user's list 404", method: http.MethodPatch, path: "/api/v1/watchlists/1", session: adminSession, body: `{"name":"Mine"}`, wantStatus: 404, wantCode: domain.CodeWatchlistNotFound},
		{name: "remove 204", method: http.MethodDelete, path: "/api/v1/watchlists/1/items/SIM-NVE", session: memberSession, wantStatus: 204},
		{name: "remove absent item 404", method: http.MethodDelete, path: "/api/v1/watchlists/1/items/SIM-FJRD", session: memberSession, wantStatus: 404, wantCode: domain.CodeWatchlistItemNotFound},
		{name: "remove from another user's list 404", method: http.MethodDelete, path: "/api/v1/watchlists/1/items/SIM-NVE", session: adminSession, wantStatus: 404, wantCode: domain.CodeWatchlistNotFound},
		{name: "delete 204", method: http.MethodDelete, path: "/api/v1/watchlists/1", session: memberSession, wantStatus: 204},
		{name: "delete another user's list 404", method: http.MethodDelete, path: "/api/v1/watchlists/1", session: adminSession, wantStatus: 404, wantCode: domain.CodeWatchlistNotFound},
		{name: "delete non-numeric id 400", method: http.MethodDelete, path: "/api/v1/watchlists/xyz", session: memberSession, wantStatus: 400, wantCode: domain.CodeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testutil.NewWatchlistRepo()
			seedEnergy(t, repo)
			if tt.path == "/api/v1/watchlists/3/items" {
				// A second, empty list for the member.
				if _, err := repo.Create(context.Background(), 1, "Empty", domain.MaxWatchlists); err != nil {
					t.Fatal(err)
				}
			}
			w := do(t, watchlistEngine(repo, nveMarket()), tt.method, tt.path, tt.session, tt.body)
			expectStatus(t, w, tt.wantStatus)
			if tt.wantCode != "" {
				expectErrorCode(t, w, tt.wantCode)
			}
		})
	}
}

func TestWatchlistHandler_CreateBodyAndLimit(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	r := watchlistEngine(repo, nveMarket())

	w := do(t, r, http.MethodPost, "/api/v1/watchlists", memberSession, `{"name":"Energy"}`)
	expectStatus(t, w, http.StatusCreated)
	body := decodeJSON(t, w.Body.Bytes())
	if body["name"] != "Energy" || body["id"] == nil {
		t.Errorf("body = %s", w.Body.String())
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Errorf("items must be [], got %s", w.Body.String())
	}

	for i := 2; i <= domain.MaxWatchlists; i++ {
		expectStatus(t, do(t, r, http.MethodPost, "/api/v1/watchlists", memberSession, `{"name":"L"}`), http.StatusCreated)
	}
	w = do(t, r, http.MethodPost, "/api/v1/watchlists", memberSession, `{"name":"Eleventh"}`)
	expectStatus(t, w, http.StatusUnprocessableEntity)
	expectErrorCode(t, w, domain.CodeWatchlistLimitReached)
}

func TestWatchlistHandler_GetItemShape(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	seedEnergy(t, repo)
	w := do(t, watchlistEngine(repo, nveMarket()), http.MethodGet, "/api/v1/watchlists/1", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	raw := w.Body.String()
	for _, want := range []string{
		`"id":1`, `"name":"Energy"`, `"orderbook_id":"SIM-NVE"`, `"symbol":"NVE"`, `"name":"Nordvik Energy"`,
		`"last_price":100.00`, `"change_percent":-0.42`, `"added_at":"2026-11-08T09:00:02Z"`, `"created_at":"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("body lacks %s: %s", want, raw)
		}
	}
}

func TestWatchlistHandler_NullPriceWhenQuoteFails(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	seedEnergy(t, repo)
	provider := &testutil.MarketProvider{QuoteFn: func(context.Context, string) (domain.Quote, error) {
		return domain.Quote{}, domain.ErrMarketDataUnavailable
	}}
	w := do(t, watchlistEngine(repo, provider), http.MethodGet, "/api/v1/watchlists", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	raw := w.Body.String()
	for _, want := range []string{`"last_price":null`, `"change_percent":null`, `"next_cursor":null`} {
		if !strings.Contains(raw, want) {
			t.Errorf("body lacks %s: %s", want, raw)
		}
	}
}

func TestWatchlistHandler_ListOnlyOwnLists(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	seedEnergy(t, repo)
	w := do(t, watchlistEngine(repo, nveMarket()), http.MethodGet, "/api/v1/watchlists", adminSession, nil)
	expectStatus(t, w, http.StatusOK)
	body := decodeJSON(t, w.Body.Bytes())
	items, _ := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "Admin" {
		t.Errorf("admin sees %s, want only list Admin", w.Body.String())
	}
}

func TestWatchlistHandler_AddItemBody(t *testing.T) {
	repo := testutil.NewWatchlistRepo()
	seedEnergy(t, repo)
	if _, err := repo.Create(context.Background(), 1, "Empty", domain.MaxWatchlists); err != nil {
		t.Fatal(err)
	}
	w := do(t, watchlistEngine(repo, nveMarket()), http.MethodPost, "/api/v1/watchlists/3/items", memberSession, `{"orderbook_id":"SIM-NVE"}`)
	expectStatus(t, w, http.StatusCreated)
	raw := w.Body.String()
	for _, want := range []string{`"orderbook_id":"SIM-NVE"`, `"symbol":"NVE"`, `"name":"Nordvik Energy"`, `"last_price":100.00`, `"added_at":"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("body lacks %s: %s", want, raw)
		}
	}
}

func TestWatchlistHandler_UnexpectedErrorsAre500AndMarketFailures502(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(repo *testutil.WatchlistRepo, provider *testutil.MarketProvider)
		method     string
		path       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{
			name: "list db error",
			setup: func(r *testutil.WatchlistRepo, _ *testutil.MarketProvider) {
				r.ListFn = func(context.Context, uint) ([]domain.Watchlist, error) { return nil, errors.New("db down") }
			},
			method: http.MethodGet, path: "/api/v1/watchlists", wantStatus: 500, wantCode: domain.CodeInternal,
		},
		{
			name: "add with market data down",
			setup: func(_ *testutil.WatchlistRepo, p *testutil.MarketProvider) {
				p.DetailsFn = func(context.Context, string) (domain.InstrumentDetails, error) {
					return domain.InstrumentDetails{}, errors.New("timeout")
				}
			},
			method: http.MethodPost, path: "/api/v1/watchlists/1/items", body: `{"orderbook_id":"SIM-FJRD"}`, wantStatus: 502, wantCode: domain.CodeMarketDataUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := testutil.NewWatchlistRepo()
			seedEnergy(t, repo)
			provider := nveMarket()
			tt.setup(repo, provider)
			w := do(t, watchlistEngine(repo, provider), tt.method, tt.path, memberSession, tt.body)
			expectStatus(t, w, tt.wantStatus)
			expectErrorCode(t, w, tt.wantCode)
		})
	}
}
