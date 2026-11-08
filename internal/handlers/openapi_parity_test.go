package handlers_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"backend/api"
	"backend/internal/app"
	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/handlers"
	"backend/internal/services"
	"backend/internal/testutil"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
)

// Story NW-07: api/openapi.yaml is the enforced source of truth. These tests build the same engine cmd/api
// serves (app.App.Router) from in-memory fakes, with every optional route switched on, and compare it with
// the embedded spec in both directions.

// routesOutsideSpec are mounted but deliberately not in the spec: the docs page is not part of /api/v1.
var routesOutsideSpec = map[string]bool{
	"GET /api/docs": true,
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true, "head": true, "options": true}

type specDoc struct {
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas   map[string]map[string]any `yaml:"schemas"`
		Responses map[string]map[string]any `yaml:"responses"`
	} `yaml:"components"`
}

func loadSpec(t *testing.T) specDoc {
	t.Helper()
	var doc specDoc
	if err := yaml.Unmarshal(api.OpenAPISpec, &doc); err != nil {
		t.Fatalf("parse api/openapi.yaml: %v", err)
	}
	if len(doc.Servers) != 1 {
		t.Fatalf("want exactly one server in api/openapi.yaml, got %d", len(doc.Servers))
	}
	return doc
}

// fullApp wires every handler like internal/app.NewApp does, but on fakes and with guest login and sim
// control enabled, so that every route that can exist on some deployment is mounted.
func fullApp() *app.App {
	cfg := &config.Config{Auth: config.Auth{
		FrontendURL:             "http://frontend.test",
		SessionTTL:              time.Hour,
		OAuthStateTTL:           time.Minute,
		GuestEnabled:            true,
		GuestSessionTTL:         time.Hour,
		GuestRateLimitPerMinute: 10,
	}}
	users := testutil.NewUserRepo()
	auth := testutil.NewAuthRepo()
	provider := &testutil.MarketProvider{}
	return &app.App{
		HealthHandler:    handlers.NewHealthHandler(domain.Features{GuestAuth: true, SimControl: true}, config.MarketProviderSimulated),
		OpenAPIHandler:   handlers.NewOpenAPIHandler(api.OpenAPISpec),
		UserHandler:      handlers.NewUserHandler(services.NewUserService(users), auth),
		MarketHandler:    handlers.NewMarketHandler(services.NewMarketService(provider)),
		AuthHandler:      handlers.NewAuthHandler(services.NewAuthService(users, auth, cfg), cfg),
		PortfolioHandler: handlers.NewPortfolioHandler(services.NewPortfolioService(&testutil.PortfolioRepo{}, provider, users), auth),
		OrderHandler:     handlers.NewOrderHandler(services.NewOrderService(&testutil.OrderRepo{}, provider), auth),
		WatchlistHandler: handlers.NewWatchlistHandler(services.NewWatchlistService(testutil.NewWatchlistRepo(), provider), auth),
		AnalyticsHandler: handlers.NewAnalyticsHandler(services.NewAnalyticsService(&testutil.SnapshotRepo{},
			services.NewPortfolioService(&testutil.PortfolioRepo{}, provider, users), 0.02, nil), auth),
		LeaderboardHandler: handlers.NewLeaderboardHandler(services.NewLeaderboardService(testutil.NewLeaderboardRepo(), users,
			services.NewAnalyticsService(&testutil.SnapshotRepo{}, services.NewPortfolioService(&testutil.PortfolioRepo{}, provider, users), 0.02, nil)), auth),
		SimHandler: handlers.NewSimHandler(&testutil.SimControl{}),
		AuthRepo:   auth,
	}
}

var ginParam = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// mountedRoutes returns "METHOD /spec/path" for every route of the engine under the server URL, plus the
// routes outside it in `outside` (as "METHOD /full/path").
func mountedRoutes(t *testing.T, engine *gin.Engine, base string) (inSpec map[string]bool, outside []string) {
	t.Helper()
	inSpec = map[string]bool{}
	for _, r := range engine.Routes() {
		path := ginParam.ReplaceAllString(r.Path, "{$1}")
		if rel, ok := strings.CutPrefix(path, base); ok && strings.HasPrefix(rel, "/") {
			inSpec[r.Method+" "+rel] = true
			continue
		}
		outside = append(outside, r.Method+" "+path)
	}
	return inSpec, outside
}

func specOperations(doc specDoc) map[string]bool {
	ops := map[string]bool{}
	for path, item := range doc.Paths {
		for method := range item {
			if httpMethods[method] {
				ops[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	return ops
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestOpenAPIRouteParity(t *testing.T) {
	doc := loadSpec(t)
	base := doc.Servers[0].URL
	mounted, outside := mountedRoutes(t, fullApp().Router(), base)
	documented := specOperations(doc)

	t.Run("every mounted route is in the spec", func(t *testing.T) {
		for _, route := range sortedKeys(mounted) {
			if !documented[route] {
				t.Errorf("%s (under %s) is mounted but not documented in api/openapi.yaml", route, base)
			}
		}
	})

	t.Run("every documented operation is mounted", func(t *testing.T) {
		for _, op := range sortedKeys(documented) {
			if !mounted[op] {
				t.Errorf("%s is documented in api/openapi.yaml but no route serves %s%s", op, base, strings.SplitN(op, " ", 2)[1])
			}
		}
	})

	t.Run("routes outside the server URL are the known ones", func(t *testing.T) {
		got := map[string]bool{}
		for _, r := range outside {
			got[r] = true
			if !routesOutsideSpec[r] {
				t.Errorf("%s is mounted outside %s and is neither documented nor allow-listed", r, base)
			}
		}
		for r := range routesOutsideSpec {
			if !got[r] {
				t.Errorf("%s is allow-listed but not mounted", r)
			}
		}
	})

	t.Run("the parity check itself sees the core NW-01…06 routes", func(t *testing.T) {
		for _, op := range []string{
			"POST /auth/guest", "GET /market/instruments", "GET /orders", "POST /orders",
			"GET /portfolio", "GET /watchlists/{id}", "PUT /sim/instruments/{orderbook_id}/price", "GET /openapi.yaml",
		} {
			if !mounted[op] || !documented[op] {
				t.Errorf("%s: mounted=%t documented=%t, want both", op, mounted[op], documented[op])
			}
		}
	})
}

// A route the spec documents must also answer through the real router, not just be registered: the served
// spec and docs page are reachable on the full engine.
func TestRouterServesSpecAndDocs(t *testing.T) {
	engine := fullApp().Router()
	for path, wantType := range map[string]string{
		"/api/v1/openapi.yaml": handlers.OpenAPIContentType,
		"/api/docs":            "text/html; charset=utf-8",
	} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != wantType {
			t.Errorf("GET %s = %d %q, want 200 %q", path, w.Code, w.Header().Get("Content-Type"), wantType)
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))
	if w.Body.String() != string(api.OpenAPISpec) {
		t.Error("GET /api/v1/openapi.yaml must serve the embedded api/openapi.yaml byte for byte")
	}
}

// Every error response the spec documents (status >= 400) that has a body uses the Error schema.
func TestOpenAPIErrorResponsesUseErrorSchema(t *testing.T) {
	doc := loadSpec(t)
	const errorRef = "#/components/schemas/Error"
	for path, item := range doc.Paths {
		for method, rawOp := range item {
			if !httpMethods[method] {
				continue
			}
			op, _ := rawOp.(map[string]any)
			responses, _ := op["responses"].(map[string]any)
			for status, rawResp := range responses {
				code, err := strconv.Atoi(status)
				if err != nil || code < 400 {
					continue
				}
				resp, _ := rawResp.(map[string]any)
				if ref, ok := resp["$ref"].(string); ok {
					resp = doc.Components.Responses[strings.TrimPrefix(ref, "#/components/responses/")]
					if resp == nil {
						t.Errorf("%s %s %s: unresolved $ref %s", strings.ToUpper(method), path, status, ref)
						continue
					}
				}
				content, _ := resp["content"].(map[string]any)
				if content == nil {
					continue // e.g. 404 when guest login is disabled: the route does not exist, gin answers.
				}
				media, _ := content["application/json"].(map[string]any)
				schema, _ := media["schema"].(map[string]any)
				if schema["$ref"] != errorRef {
					t.Errorf("%s %s %s: error body schema = %v, want $ref %s", strings.ToUpper(method), path, status, schema, errorRef)
				}
			}
		}
	}
}

// domainErrorCodes collects every `Code* = "..."` string constant declared in internal/domain.
func domainErrorCodes(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "domain", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list internal/domain: %v", err)
	}
	codes := map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Code") || i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						v, _ := strconv.Unquote(lit.Value)
						codes[v] = true
					}
				}
			}
		}
	}
	return codes
}

func TestOpenAPIErrorCodesMatchDomain(t *testing.T) {
	doc := loadSpec(t)
	rawEnum, _ := doc.Components.Schemas["ErrorCode"]["enum"].([]any)
	enum := map[string]bool{}
	for _, v := range rawEnum {
		s, _ := v.(string)
		enum[s] = true
	}
	codes := domainErrorCodes(t)
	if len(codes) < 14 {
		t.Fatalf("found only %d domain.Code* constants; the scan is broken", len(codes))
	}
	for _, c := range sortedKeys(codes) {
		if !enum[c] {
			t.Errorf("domain code %s is missing from #/components/schemas/ErrorCode", c)
		}
	}
	for _, c := range sortedKeys(enum) {
		if !codes[c] {
			t.Errorf("ErrorCode %s is in the spec but no domain.Code* constant has that value", c)
		}
	}
}
