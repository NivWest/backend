package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

// LeaderboardHandler serves the public leaderboard endpoint (story NW-13).
type LeaderboardHandler struct {
	service *services.LeaderboardService
	auth    domain.AuthRepository
}

func NewLeaderboardHandler(service *services.LeaderboardService, auth domain.AuthRepository) *LeaderboardHandler {
	return &LeaderboardHandler{
		service: service,
		auth:    auth,
	}
}

func (h *LeaderboardHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/leaderboard", h.GetLeaderboard)
}

func (h *LeaderboardHandler) GetLeaderboard(c *gin.Context) {
	metric := c.Query("metric")
	if metric != domain.LeaderboardMetricReturn && metric != domain.LeaderboardMetricSharpe {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{
			Error: "metric must be one of: return, sharpe",
			Code:  domain.CodeValidation,
		})
		return
	}

	limit := domain.DefaultLeaderboardLimit
	if l := c.Query("limit"); l != "" {
		val, err := strconv.Atoi(l)
		if err != nil || val < 1 || val > domain.MaxLeaderboardLimit {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{
				Error: "limit must be between 1 and 100",
				Code:  domain.CodeValidation,
			})
			return
		}
		limit = val
	}

	var callerID uint
	if user, ok := middleware.CurrentUser(c); ok {
		callerID = user.ID
	} else if h.auth != nil {
		if cookie, err := c.Cookie("session_id"); err == nil && cookie != "" {
			if u, err := h.auth.GetSessionUser(c.Request.Context(), cookie); err == nil && u != nil {
				callerID = u.ID
			}
		}
	}

	page, err := h.service.GetLeaderboard(c.Request.Context(), metric, limit, callerID)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidMetric) {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{
				Error: "invalid metric",
				Code:  domain.CodeValidation,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{
			Error: "failed to get leaderboard",
			Code:  domain.CodeInternal,
		})
		return
	}

	c.JSON(http.StatusOK, page)
}
