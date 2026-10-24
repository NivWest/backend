package handlers

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/version"

	"github.com/gin-gonic/gin"
)

// HealthHandler reports liveness, the running build version, the enabled features and the active
// market data provider. Used by local stacks, E2E runs and (later) Cloud Run health checks.
type HealthHandler struct {
	features           domain.Features
	marketDataProvider string
}

// NewHealthHandler takes the deployment's feature switches and the active market data provider
// ("avanza" or "simulated", MARKET_DATA_PROVIDER).
func NewHealthHandler(features domain.Features, marketDataProvider string) *HealthHandler {
	return &HealthHandler{features: features, marketDataProvider: marketDataProvider}
}

func (h *HealthHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/health", h.Health)
}

func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, domain.HealthResponse{
		Status:   "ok",
		Version:  version.Version,
		Features: h.features,

		MarketDataProvider: h.marketDataProvider,
	})
}
