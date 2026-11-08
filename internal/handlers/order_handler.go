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
	"strings"

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
	orders.GET("/:id", h.GetOrder)
	orders.DELETE("/:id", h.CancelOrder)
	orders.PATCH("/:id/note", h.UpdateOrderNote)
}

// GetOrder handles GET /orders/{id}: 200 with the signed-in user's order, 404 ORDER_NOT_FOUND for an unknown
// or another user's id.
func (h *OrderHandler) GetOrder(c *gin.Context) {
	user, orderID, ok := h.orderTarget(c)
	if !ok {
		return
	}
	order, err := h.service.GetOrder(c.Request.Context(), user, orderID)
	if err != nil {
		writeOrderError(c, user, err)
		return
	}
	c.JSON(http.StatusOK, order)
}

// CancelOrder handles DELETE /orders/{id}: 200 with the CANCELLED order, 409 ORDER_NOT_OPEN once it left OPEN,
// 404 ORDER_NOT_FOUND for an unknown or another user's id.
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	user, orderID, ok := h.orderTarget(c)
	if !ok {
		return
	}
	order, err := h.service.CancelOrder(c.Request.Context(), user, orderID)
	if err != nil {
		writeOrderError(c, user, err)
		return
	}
	c.JSON(http.StatusOK, order)
}

// orderTarget reads the signed-in user and the {id} path parameter, answering 401 or 400 itself when either
// is missing or invalid.
func (h *OrderHandler) orderTarget(c *gin.Context) (userID, orderID uint, ok bool) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return 0, 0, false
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 || id > uint64(^uint(0)) {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "order id must be a positive integer", Code: domain.CodeValidation})
		return 0, 0, false
	}
	return user.ID, uint(id), true
}

// ListOrders handles GET /orders?limit=&cursor=&status=: 200 with one OrderPage of the signed-in user's orders,
// newest first. A missing limit means domain.DefaultOrderPageLimit; the cursor is passed on unchanged; status is
// a comma-separated list of OrderStatus values (story NW-08).
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

	var statuses []domain.OrderStatus
	if raw, present := c.GetQuery("status"); present {
		for _, part := range strings.Split(raw, ",") {
			statuses = append(statuses, domain.OrderStatus(strings.TrimSpace(part)))
		}
	}

	page, err := h.service.ListOrders(c.Request.Context(), user.ID, limit, c.Query("cursor"), statuses...)
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

// PlaceOrder handles POST /orders: 201 with the executed MARKET Order or the OPEN LIMIT/STOP Order, or 200 with
// the original Order when the Idempotency-Key replays an earlier request.
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
// a quantity, limit_price or stop_price sent as a string (which decimal.Decimal would otherwise accept) are
// rejected; a null price counts as absent.
func decodeOrderRequest(c *gin.Context) (domain.OrderRequest, error) {
	var req domain.OrderRequest
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxOrderBodyBytes))
	if err != nil {
		return req, errors.New("request body is too large or unreadable")
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return req, errors.New("request body must be a JSON OrderRequest {orderbook_id, side, quantity, type, limit_price, stop_price}")
	}
	var fields struct {
		Quantity   json.RawMessage `json:"quantity"`
		LimitPrice json.RawMessage `json:"limit_price"`
		StopPrice  json.RawMessage `json:"stop_price"`
	}
	_ = json.Unmarshal(raw, &fields) // cannot fail: raw already decoded as an object above
	if q := bytes.TrimSpace(fields.Quantity); len(q) > 0 && (q[0] == '"' || bytes.Equal(q, []byte("null"))) {
		return req, errors.New("quantity must be a number")
	}
	for name, v := range map[string]json.RawMessage{"limit_price": fields.LimitPrice, "stop_price": fields.StopPrice} {
		if p := bytes.TrimSpace(v); len(p) > 0 && p[0] == '"' {
			return req, errors.New(name + " must be a number")
		}
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
	case errors.Is(err, domain.ErrOrderNotOpen):
		c.JSON(http.StatusConflict, domain.ErrorResponse{Error: domain.ErrOrderNotOpen.Error(), Code: domain.CodeOrderNotOpen})
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
