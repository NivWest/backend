package app

import (
	"github.com/gin-gonic/gin"
)

// Registrar mounts a handler's routes on the /api/v1 group.
type Registrar interface {
	Register(v1 *gin.RouterGroup)
}

// Router builds the HTTP engine with every route of the API: the handlers under /api/v1 and the
// interactive API reference at /api/docs. cmd/api serves it, and the route parity test
// (internal/handlers/openapi_parity_test.go) checks it against api/openapi.yaml, so both see the same routes.
func (a *App) Router(middleware ...gin.HandlerFunc) *gin.Engine {
	engine := gin.New()
	engine.Use(middleware...)

	api := engine.Group("/api")
	v1 := api.Group("/v1")
	for _, r := range a.registrars() {
		r.Register(v1)
	}
	if a.OpenAPIHandler != nil {
		a.OpenAPIHandler.RegisterDocs(api)
	}
	return engine
}

// registrars lists the handlers that exist on this deployment. Optional handlers are nil when switched
// off; a nil pointer in a Registrar interface would not be nil and would still mount, so skip them here.
func (a *App) registrars() []Registrar {
	var out []Registrar
	add := func(isNil bool, r Registrar) {
		if !isNil {
			out = append(out, r)
		}
	}
	add(a.HealthHandler == nil, a.HealthHandler)
	add(a.OpenAPIHandler == nil, a.OpenAPIHandler)
	add(a.MarketHandler == nil, a.MarketHandler)
	add(a.MarketStreamHandler == nil, a.MarketStreamHandler)
	add(a.UserHandler == nil, a.UserHandler)
	add(a.AuthHandler == nil, a.AuthHandler)
	add(a.PortfolioHandler == nil, a.PortfolioHandler)
	add(a.OrderHandler == nil, a.OrderHandler)
	add(a.WatchlistHandler == nil, a.WatchlistHandler)
	add(a.AnalyticsHandler == nil, a.AnalyticsHandler)
	add(a.AIHandler == nil, a.AIHandler)
	// SimHandler is nil unless MARKET_DATA_PROVIDER=simulated and MARKET_SIM_CONTROL_ENABLED=true.
	add(a.SimHandler == nil, a.SimHandler)
	return out
}
