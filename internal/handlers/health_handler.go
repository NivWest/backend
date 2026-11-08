package handlers

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/version"

	"github.com/gin-gonic/gin"
)

// HealthHandler reports liveness, the running build version, the enabled features and the active
// market data and LLM providers. Used by local stacks, E2E runs and (later) Cloud Run health checks.
type HealthHandler struct {
	features           domain.Features
	marketDataProvider string
	llmProvider        string
}

// NewHealthHandler takes the deployment's feature switches, the active market data provider
// ("avanza" or "simulated", MARKET_DATA_PROVIDER) and the active LLM provider ("anthropic" or "fake", LLM_PROVIDER).
func NewHealthHandler(features domain.Features, marketDataProvider, llmProvider string) *HealthHandler {
	return &HealthHandler{features: features, marketDataProvider: marketDataProvider, llmProvider: llmProvider}
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
		LLMProvider:        h.llmProvider,
	})
}
