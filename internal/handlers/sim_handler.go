package handlers

import (
	"encoding/json"
	"net/http"

	"backend/internal/domain"
	"github.com/gin-gonic/gin"
)

const errPriceNotPositive = "price must be greater than 0"

// SimHandler pins and clears prices on the simulated market (PUT/DELETE /sim/instruments/:orderbook_id/price).
//
// DEV/TEST ONLY: internal/app mounts it only when MARKET_SIM_CONTROL_ENABLED=true together with the
// simulated provider (MARKET_DATA_PROVIDER=simulated). It has no auth and no rate limit, so it must
// never be mounted in production.
type SimHandler struct {
	control domain.MarketSimControl
}

func NewSimHandler(control domain.MarketSimControl) *SimHandler {
	return &SimHandler{control: control}
}

func (h *SimHandler) Register(v1 *gin.RouterGroup) {
	sim := v1.Group("/sim")
	sim.PUT("/instruments/:orderbook_id/price", h.SetPrice)
	sim.DELETE("/instruments/:orderbook_id/price", h.ClearPrice)
}

// SetPrice handles PUT /sim/instruments/:orderbook_id/price with body {"price": <number > 0>}.
func (h *SimHandler) SetPrice(c *gin.Context) {
	var req domain.SimPriceRequest
	if c.Request.Body == nil || json.NewDecoder(c.Request.Body).Decode(&req) != nil || !(req.Price > 0) {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: errPriceNotPositive, Code: domain.CodeValidation})
		return
	}
	if err := h.control.SetPrice(c.Request.Context(), c.Param("orderbook_id"), req.Price); err != nil {
		writeMarketError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ClearPrice handles DELETE /sim/instruments/:orderbook_id/price. It is idempotent.
func (h *SimHandler) ClearPrice(c *gin.Context) {
	if err := h.control.ClearPrice(c.Request.Context(), c.Param("orderbook_id")); err != nil {
		writeMarketError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
