package handlers

import (
	"backend/internal/version"
	"github.com/gin-gonic/gin"
	"net/http"
)

// HealthHandler reports liveness and the running build version.
// Used by local stacks, E2E runs and (later) Cloud Run health checks.
type HealthHandler struct{}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

func (h *HealthHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/health", h.Health)
}

func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "version": version.Version})
}
