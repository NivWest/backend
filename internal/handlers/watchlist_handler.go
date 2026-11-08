package handlers

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/models"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

// maxWatchlistBodyBytes caps watchlist request bodies; a valid one is a few dozen bytes.
const maxWatchlistBodyBytes = 16 << 10

// WatchlistHandler serves /watchlists (story NW-06). Every route needs a session and only sees the caller's
// own lists; another user's list id answers 404 WATCHLIST_NOT_FOUND.
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

	lists.GET("", h.List)
	lists.POST("", h.Create)
	lists.GET("/:id", h.Get)
	lists.PATCH("/:id", h.Rename)
	lists.DELETE("/:id", h.Delete)

	lists.POST("/:id/items", h.AddItem)
	lists.DELETE("/:id/items/:orderbook_id", h.RemoveItem)
}

// List handles GET /watchlists.
func (h *WatchlistHandler) List(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	res, err := h.service.List(c.Request.Context(), user.ID)
	if err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Create handles POST /watchlists: 201 with the new, empty Watchlist.
func (h *WatchlistHandler) Create(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	var req domain.WatchlistNameRequest
	if !bindWatchlistBody(c, &req, "request body must be a JSON object {name}") {
		return
	}
	wl, err := h.service.Create(c.Request.Context(), user.ID, req)
	if err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusCreated, wl)
}

// Get handles GET /watchlists/{id}.
func (h *WatchlistHandler) Get(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	id, ok := watchlistID(c)
	if !ok {
		return
	}
	wl, err := h.service.Get(c.Request.Context(), user.ID, id)
	if err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, wl)
}

// Rename handles PATCH /watchlists/{id}: 200 with the renamed Watchlist.
func (h *WatchlistHandler) Rename(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	id, ok := watchlistID(c)
	if !ok {
		return
	}
	var req domain.WatchlistNameRequest
	if !bindWatchlistBody(c, &req, "request body must be a JSON object {name}") {
		return
	}
	wl, err := h.service.Rename(c.Request.Context(), user.ID, id, req)
	if err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, wl)
}

// Delete handles DELETE /watchlists/{id}: 204.
func (h *WatchlistHandler) Delete(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	id, ok := watchlistID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), user.ID, id); err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AddItem handles POST /watchlists/{id}/items: 201 with the WatchlistItem.
func (h *WatchlistHandler) AddItem(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	id, ok := watchlistID(c)
	if !ok {
		return
	}
	var req domain.WatchlistItemRequest
	if !bindWatchlistBody(c, &req, "request body must be a JSON object {orderbook_id}") {
		return
	}
	item, err := h.service.AddItem(c.Request.Context(), user.ID, id, req)
	if err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

// RemoveItem handles DELETE /watchlists/{id}/items/{orderbook_id}: 204.
func (h *WatchlistHandler) RemoveItem(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	id, ok := watchlistID(c)
	if !ok {
		return
	}
	if err := h.service.RemoveItem(c.Request.Context(), user.ID, id, c.Param("orderbook_id")); err != nil {
		writeWatchlistError(c, user.ID, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// currentUserOr401 returns the signed-in user, or answers 401 UNAUTHENTICATED.
func currentUserOr401(c *gin.Context) (*models.User, bool) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return nil, false
	}
	return user, true
}

// watchlistID parses the :id path parameter (a positive integer); otherwise it answers 400 VALIDATION_ERROR.
func watchlistID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "watchlist id must be a positive integer", Code: domain.CodeValidation})
		return 0, false
	}
	return uint(id), true
}

// bindWatchlistBody decodes a JSON body into dst; malformed or oversized bodies answer 400 VALIDATION_ERROR.
func bindWatchlistBody(c *gin.Context, dst any, msg string) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWatchlistBodyBytes)
	if err := c.ShouldBindJSON(dst); err != nil {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: msg, Code: domain.CodeValidation})
		return false
	}
	return true
}

// writeWatchlistError maps WatchlistService errors to their HTTP status and domain.ErrorResponse body.
// Unexpected errors are logged with the user id only (never the session or the email).
func writeWatchlistError(c *gin.Context, userID uint, err error) {
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: validation.Msg, Code: domain.CodeValidation})
	case errors.Is(err, domain.ErrWatchlistNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: domain.ErrWatchlistNotFound.Error(), Code: domain.CodeWatchlistNotFound})
	case errors.Is(err, domain.ErrWatchlistItemNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: domain.ErrWatchlistItemNotFound.Error(), Code: domain.CodeWatchlistItemNotFound})
	case errors.Is(err, domain.ErrInstrumentNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: domain.ErrInstrumentNotFound.Error(), Code: domain.CodeInstrumentNotFound})
	case errors.Is(err, domain.ErrDuplicateItem):
		c.JSON(http.StatusConflict, domain.ErrorResponse{Error: domain.ErrDuplicateItem.Error(), Code: domain.CodeConflict})
	case errors.Is(err, domain.ErrWatchlistLimit):
		c.JSON(http.StatusUnprocessableEntity, domain.ErrorResponse{Error: domain.ErrWatchlistLimit.Error(), Code: domain.CodeWatchlistLimitReached})
	case errors.Is(err, domain.ErrMarketDataUnavailable):
		log.Printf("[WATCHLIST] market data failure for user_id=%d: %v", userID, err)
		c.JSON(http.StatusBadGateway, domain.ErrorResponse{Error: "market data unavailable", Code: domain.CodeMarketDataUnavailable})
	default:
		log.Printf("[WATCHLIST] internal error for user_id=%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
	}
}
