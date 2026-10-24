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

// MarketHandler serves the public market data routes under /market (no session needed).
// Every route shares the per-IP RateLimit budget, which protects the upstream provider.
type MarketHandler struct {
	service *services.MarketService
}

func NewMarketHandler(service *services.MarketService) *MarketHandler {
	return &MarketHandler{service: service}
}

func (h *MarketHandler) Register(v1 *gin.RouterGroup) {
	market := v1.Group("/market")
	market.Use(middleware.RateLimit())

	market.GET("/instruments", h.Search)
	market.GET("/instruments/:orderbook_id", h.Details)
	market.GET("/instruments/:orderbook_id/quote", h.Quote)
	market.GET("/instruments/:orderbook_id/candles", h.Candles)
}

// Search handles GET /market/instruments?q=.
func (h *MarketHandler) Search(c *gin.Context) {
	items, err := h.service.Search(c.Request.Context(), c.Query("q"))
	if err != nil {
		writeMarketError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.InstrumentSearchResponse{Items: items, NextCursor: nil})
}

// Details handles GET /market/instruments/:orderbook_id.
func (h *MarketHandler) Details(c *gin.Context) {
	details, err := h.service.Details(c.Request.Context(), c.Param("orderbook_id"))
	if err != nil {
		writeMarketError(c, err)
		return
	}
	c.JSON(http.StatusOK, details)
}

// Quote handles GET /market/instruments/:orderbook_id/quote.
func (h *MarketHandler) Quote(c *gin.Context) {
	quote, err := h.service.Quote(c.Request.Context(), c.Param("orderbook_id"))
	if err != nil {
		writeMarketError(c, err)
		return
	}
	c.JSON(http.StatusOK, quote)
}

// Candles handles GET /market/instruments/:orderbook_id/candles?period=.
func (h *MarketHandler) Candles(c *gin.Context) {
	id := c.Param("orderbook_id")
	candles, period, err := h.service.Candles(c.Request.Context(), id, c.Query("period"))
	if err != nil {
		writeMarketError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.CandlesResponse{OrderbookID: id, Period: period, Candles: candles})
}

// writeMarketError maps market and sim errors to their HTTP status and domain.ErrorResponse body.
// Upstream details never reach the client: they are only logged.
func writeMarketError(c *gin.Context, err error) {
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: validation.Msg, Code: domain.CodeValidation})
	case errors.Is(err, domain.ErrInstrumentNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: domain.ErrInstrumentNotFound.Error(), Code: domain.CodeInstrumentNotFound})
	case errors.Is(err, domain.ErrMarketDataUnavailable):
		log.Printf("[MARKET] upstream failure on %s %s: %v", c.Request.Method, c.FullPath(), err)
		c.JSON(http.StatusBadGateway, domain.ErrorResponse{Error: "market data unavailable", Code: domain.CodeMarketDataUnavailable})
	default:
		log.Printf("[MARKET] internal error on %s %s: %v", c.Request.Method, c.FullPath(), err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
	}
}
