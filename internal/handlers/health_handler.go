package handlers

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/version"

	"github.com/gin-gonic/gin"
)

// HealthHandler reports liveness, the running build version and the enabled features.
// Used by local stacks, E2E runs and (later) Cloud Run health checks.
type HealthHandler struct {
	features domain.Features
}

func NewHealthHandler(features domain.Features) *HealthHandler {
	return &HealthHandler{features: features}
}

func (h *HealthHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/health", h.Health)
}

func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, domain.HealthResponse{
		Status:   "ok",
		Version:  version.Version,
		Features: h.features,
	})
}
