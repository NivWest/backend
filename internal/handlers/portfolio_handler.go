package handlers

import (
	"log"
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

	portfolio.GET("", h.GetPortfolio)
}

// GetPortfolio handles GET /portfolio: 200 with the signed-in user's domain.Portfolio.
func (h *PortfolioHandler) GetPortfolio(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	portfolio, err := h.service.GetPortfolio(c.Request.Context(), user.ID)
	if err != nil {
		// Log the user id only (never the session or the email).
		log.Printf("[PORTFOLIO] internal error for user_id=%d: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
		return
	}

	c.JSON(http.StatusOK, portfolio)
}
