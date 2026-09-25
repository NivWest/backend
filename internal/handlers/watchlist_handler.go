package handlers

import (
	"net/http"
	"strconv"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

type WatchlistHandler struct {
	service *services.WatchlistService
	auth    domain.AuthRepository
}

func NewWatchlistHandler(service *services.WatchlistService, auth domain.AuthRepository) *WatchlistHandler {
	return &WatchlistHandler{
		service: service,
		auth:    auth,
	}
}

func (h *WatchlistHandler) Register(v1 *gin.RouterGroup) {
	lists := v1.Group("/watchlists")
	lists.Use(middleware.RequireAuth(h.auth))

	lists.POST("/", h.Create)
	lists.GET("/", h.List)
	lists.DELETE("/:id", h.Delete)
	
	lists.POST("/:id/items", h.AddItem)
	lists.DELETE("/:id/items/:stockId", h.RemoveItem)
}

func (h *WatchlistHandler) Create(c *gin.Context) {
	user, _ := middleware.CurrentUser(c)
	var req domain.WatchlistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	wl, err := h.service.CreateWatchlist(c.Request.Context(), user.ID, req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create watchlist"})
		return
	}
	c.JSON(http.StatusCreated, wl)
}

func (h *WatchlistHandler) List(c *gin.Context) {
	user, _ := middleware.CurrentUser(c)
	wls, err := h.service.GetUserWatchlists(c.Request.Context(), user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch watchlists"})
		return
	}
	c.JSON(http.StatusOK, wls)
}

func (h *WatchlistHandler) Delete(c *gin.Context) {
	user, _ := middleware.CurrentUser(c)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	if err := h.service.DeleteWatchlist(c.Request.Context(), user.ID, uint(id)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *WatchlistHandler) AddItem(c *gin.Context) {
	user, _ := middleware.CurrentUser(c)
	wlId, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid watchlist id"})
		return
	}

	var req domain.WatchlistItemRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.AddItem(c.Request.Context(), user.ID, uint(wlId), req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *WatchlistHandler) RemoveItem(c *gin.Context) {
	user, _ := middleware.CurrentUser(c)
	wlId, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid watchlist id"})
		return
	}
	stockId, err := strconv.ParseUint(c.Param("stockId"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid stock id"})
		return
	}

	if err := h.service.RemoveItem(c.Request.Context(), user.ID, uint(wlId), uint(stockId)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
