package handlers

import (
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
	"net/http"
)

type StockHandler struct {
	service *services.StockService
}

func NewStockHandler(
	service *services.StockService,
) *StockHandler {
	return &StockHandler{
		service: service,
	}
}

func (h *StockHandler) Register(v1 *gin.RouterGroup) {
	stocks := v1.Group("/stocks")
	stocks.Use(middleware.RateLimit())

	stocks.GET("/search", h.Search)
	stocks.GET("/quote", h.Quote)
	stocks.GET("/details", h.Details)
	stocks.GET("/chart", h.PriceChart)
}

func (h *StockHandler) Search(c *gin.Context) {
	query := c.Query("q")

	result, err := h.service.Search(
		c.Request.Context(),
		query,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.Data(
		http.StatusOK,
		"application/json",
		result,
	)
}

func (h *StockHandler) PriceChart(c *gin.Context) {
	orderbookID := c.Query("orderbookID")
	timePeriod := c.Query("timePeriod")

	result, err := h.service.GetPriceChart(
		c.Request.Context(),
		orderbookID,
		timePeriod,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.Data(
		http.StatusOK,
		"application/json",
		result,
	)
}

func (h *StockHandler) Details(c *gin.Context) {
	orderbookID := c.Query("orderbookID")

	result, err := h.service.GetStockDetails(
		c.Request.Context(),
		orderbookID,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.Data(
		http.StatusOK,
		"application/json",
		result,
	)
}

func (h *StockHandler) Quote(c *gin.Context) {
	orderbookID := c.Query("orderbookID")

	result, err := h.service.GetStockQuote(
		c.Request.Context(),
		orderbookID,
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.Data(
		http.StatusOK,
		"application/json",
		result,
	)
}
