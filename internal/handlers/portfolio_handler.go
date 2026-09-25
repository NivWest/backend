package handlers

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

type PortfolioHandler struct {
	service *services.PortfolioService
	auth    domain.AuthRepository
}

func NewPortfolioHandler(service *services.PortfolioService, auth domain.AuthRepository) *PortfolioHandler {
	return &PortfolioHandler{
		service: service,
		auth:    auth,
	}
}

func (h *PortfolioHandler) Register(v1 *gin.RouterGroup) {
	portfolio := v1.Group("/portfolio")
	portfolio.Use(middleware.RequireAuth(h.auth))

	portfolio.GET("/dashboard", h.Dashboard)
}

func (h *PortfolioHandler) Dashboard(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	dashboard, err := h.service.GetDashboard(c.Request.Context(), user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch portfolio dashboard"})
		return
	}

	c.JSON(http.StatusOK, dashboard)
}
