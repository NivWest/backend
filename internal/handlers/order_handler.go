package handlers

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

type OrderHandler struct {
	service *services.OrderService
	auth    domain.AuthRepository
}

func NewOrderHandler(service *services.OrderService, auth domain.AuthRepository) *OrderHandler {
	return &OrderHandler{
		service: service,
		auth:    auth,
	}
}

func (h *OrderHandler) Register(v1 *gin.RouterGroup) {
	orders := v1.Group("/orders")
	orders.Use(middleware.RequireAuth(h.auth))

	orders.POST("/", h.PlaceOrder)
}

func (h *OrderHandler) PlaceOrder(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req domain.OrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload", "details": err.Error()})
		return
	}

	if err := h.service.ExecuteOrder(c.Request.Context(), user.ID, req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Order executed successfully"})
}
