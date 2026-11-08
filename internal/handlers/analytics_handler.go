package handlers

import (
	"errors"
	"log"
	"net/http"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

// AnalyticsHandler serves the equity history and risk analytics of the signed-in user (story NW-10).
type AnalyticsHandler struct {
	service *services.AnalyticsService
	auth    domain.AuthRepository
}

func NewAnalyticsHandler(service *services.AnalyticsService, auth domain.AuthRepository) *AnalyticsHandler {
	return &AnalyticsHandler{service: service, auth: auth}
}

func (h *AnalyticsHandler) Register(v1 *gin.RouterGroup) {
	portfolio := v1.Group("/portfolio")
	portfolio.Use(middleware.RequireAuth(h.auth))

	portfolio.GET("/history", h.GetHistory)
	portfolio.GET("/analytics", h.GetAnalytics)
}

// GetHistory handles GET /portfolio/history?range=1d|1w|1m|all (default 1d): 200 with domain.EquityHistory,
// 400 VALIDATION_ERROR for any other range (an empty ?range= included).
func (h *AnalyticsHandler) GetHistory(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	raw, present := c.GetQuery("range")
	if present && raw == "" {
		_, err := domain.ParseHistoryRange(raw)
		h.fail(c, user.ID, "history", err)
		return
	}
	history, err := h.service.GetHistory(c.Request.Context(), user.ID, raw)
	if err != nil {
		h.fail(c, user.ID, "history", err)
		return
	}
	c.JSON(http.StatusOK, history)
}

// GetAnalytics handles GET /portfolio/analytics: 200 with domain.PortfolioAnalytics.
func (h *AnalyticsHandler) GetAnalytics(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	analytics, err := h.service.GetAnalytics(c.Request.Context(), user.ID)
	if err != nil {
		h.fail(c, user.ID, "analytics", err)
		return
	}
	c.JSON(http.StatusOK, analytics)
}

func (h *AnalyticsHandler) fail(c *gin.Context, userID uint, what string, err error) {
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: validation.Msg, Code: domain.CodeValidation})
		return
	}
	// Log the user id only (never the session or the email).
	log.Printf("[ANALYTICS] %s failed for user_id=%d: %v", what, userID, err)
	c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
}
