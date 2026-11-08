package handlers

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

// InsightsHandler serves /insights, /ai/insights/review and /ai/insights/reviews (story NW-17).
type InsightsHandler struct {
	service *services.InsightsService
	auth    domain.AuthRepository
}

func NewInsightsHandler(service *services.InsightsService, auth domain.AuthRepository) *InsightsHandler {
	return &InsightsHandler{
		service: service,
		auth:    auth,
	}
}

func (h *InsightsHandler) Register(v1 *gin.RouterGroup) {
	insights := v1.Group("/insights")
	insights.Use(middleware.RequireAuth(h.auth))
	insights.GET("", h.GetInsights)

	aiInsights := v1.Group("/ai/insights")
	aiInsights.Use(middleware.RequireAuth(h.auth))
	aiInsights.POST("/review", h.GenerateReview)
	aiInsights.GET("/reviews", h.ListReviews)
}

// GetInsights handles GET /insights.
func (h *InsightsHandler) GetInsights(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	res, err := h.service.GetInsights(c.Request.Context(), user.ID)
	if err != nil {
		writeAIError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// GenerateReview handles POST /ai/insights/review.
func (h *InsightsHandler) GenerateReview(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	rev, err := h.service.GenerateReview(c.Request.Context(), user.ID)
	if err != nil {
		writeAIError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusCreated, rev)
}

// ListReviews handles GET /ai/insights/reviews.
func (h *InsightsHandler) ListReviews(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	list, err := h.service.ListReviews(c.Request.Context(), user.ID)
	if err != nil {
		writeAIError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, list)
}
