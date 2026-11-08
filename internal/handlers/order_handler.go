package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

// maxOrderBodyBytes caps the POST /orders body; a valid OrderRequest is far smaller.
const maxOrderBodyBytes = 64 << 10

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

	orders.GET("", h.ListOrders)
	orders.POST("", h.PlaceOrder)
	orders.PATCH("/:id/note", h.UpdateOrderNote)
}

// ListOrders handles GET /orders?limit=&cursor=: 200 with one OrderPage of the signed-in user's orders,
// newest first. A missing limit means domain.DefaultOrderPageLimit; the cursor is passed on unchanged.
func (h *OrderHandler) ListOrders(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	limit := domain.DefaultOrderPageLimit
	if raw, present := c.GetQuery("limit"); present {
		n, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{
				Error: fmt.Sprintf("limit must be an integer from 1 to %d", domain.MaxOrderPageLimit),
				Code:  domain.CodeValidation,
			})
			return
		}
		limit = n
	}

	page, err := h.service.ListOrders(c.Request.Context(), user.ID, limit, c.Query("cursor"))
	if err != nil {
		var validation *domain.ValidationError
		if errors.As(err, &validation) {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: validation.Msg, Code: domain.CodeValidation})
			return
		}
		log.Printf("[ORDER] list orders failed for user_id=%d: %v", user.ID, err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
		return
	}
	c.JSON(http.StatusOK, page)
}

// PlaceOrder handles POST /orders: 201 with the executed Order, or 200 with the original Order when the
// Idempotency-Key replays an earlier request.
func (h *OrderHandler) PlaceOrder(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	req, err := decodeOrderRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: err.Error(), Code: domain.CodeValidation})
		return
	}

	order, created, err := h.service.PlaceOrder(c.Request.Context(), user.ID, c.GetHeader(domain.IdempotencyKeyHeader), req)
	if err != nil {
		writeOrderError(c, user.ID, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, order)
}

// decodeOrderRequest reads the JSON body into a domain.OrderRequest. Malformed JSON, wrong field types and
// a quantity sent as a string (which decimal.Decimal would otherwise accept) are rejected.
func decodeOrderRequest(c *gin.Context) (domain.OrderRequest, error) {
	var req domain.OrderRequest
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxOrderBodyBytes))
	if err != nil {
		return req, errors.New("request body is too large or unreadable")
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, errors.New("request body must be a JSON OrderRequest {orderbook_id, side, quantity, type}")
	}
	var fields struct {
		Quantity json.RawMessage `json:"quantity"`
	}
	_ = json.Unmarshal(raw, &fields) // cannot fail: raw already decoded as an object above
	if q := bytes.TrimSpace(fields.Quantity); len(q) > 0 && (q[0] == '"' || bytes.Equal(q, []byte("null"))) {
		return req, errors.New("quantity must be a number")
	}
	return req, nil
}

// UpdateOrderNote handles PATCH /orders/:id/note (story NW-17).
func (h *OrderHandler) UpdateOrderNote(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	orderID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || orderID64 == 0 {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "invalid order id", Code: domain.CodeValidation})
		return
	}

	var req struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "request body must be {note: string}", Code: domain.CodeValidation})
		return
	}

	order, err := h.service.UpdateNote(c.Request.Context(), user.ID, uint(orderID64), req.Note)
	if err != nil {
		writeOrderError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, order)
}

// writeOrderError maps PlaceOrder errors to their HTTP status and domain.ErrorResponse body. Unexpected
// errors are logged with the user id only (never the session or the email).
func writeOrderError(c *gin.Context, userID uint, err error) {
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: validation.Msg, Code: domain.CodeValidation})
	case errors.Is(err, domain.ErrOrderNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: "order not found", Code: domain.CodeOrderNotFound})
	case errors.Is(err, domain.ErrInstrumentNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: domain.ErrInstrumentNotFound.Error(), Code: domain.CodeInstrumentNotFound})
	case errors.Is(err, domain.ErrInsufficientFunds):
		c.JSON(http.StatusUnprocessableEntity, domain.ErrorResponse{Error: "not enough cash for this order", Code: domain.CodeInsufficientFunds})
	case errors.Is(err, domain.ErrInsufficientShares):
		c.JSON(http.StatusUnprocessableEntity, domain.ErrorResponse{Error: "not enough shares to sell", Code: domain.CodeInsufficientShares})
	case errors.Is(err, domain.ErrIdempotencyKeyReused):
		c.JSON(http.StatusConflict, domain.ErrorResponse{Error: domain.ErrIdempotencyKeyReused.Error(), Code: domain.CodeConflict})
	case errors.Is(err, domain.ErrMarketDataUnavailable):
		log.Printf("[ORDER] market data failure for user_id=%d: %v", userID, err)
		c.JSON(http.StatusBadGateway, domain.ErrorResponse{Error: "market data unavailable", Code: domain.CodeMarketDataUnavailable})
	default:
		log.Printf("[ORDER] internal error for user_id=%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
	}
}
