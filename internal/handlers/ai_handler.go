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

// AIHandler serves /ai (story NW-14): the instrument brief and the caller's LLM usage. Every route needs a
// session; model calls are paid from the caller's daily token budget.
type AIHandler struct {
	briefs  *services.AIBriefService
	gateway *services.AIGateway
	auth    domain.AuthRepository
}

func NewAIHandler(briefs *services.AIBriefService, gateway *services.AIGateway, auth domain.AuthRepository) *AIHandler {
	return &AIHandler{briefs: briefs, gateway: gateway, auth: auth}
}

func (h *AIHandler) Register(v1 *gin.RouterGroup) {
	ai := v1.Group("/ai")
	ai.Use(middleware.RequireAuth(h.auth))
	ai.GET("/instruments/:orderbook_id/brief", h.Brief)
	ai.GET("/usage", h.Usage)
}

// Brief handles GET /ai/instruments/{orderbook_id}/brief.
func (h *AIHandler) Brief(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	brief, err := h.briefs.Brief(c.Request.Context(), user.ID, c.Param("orderbook_id"))
	if err != nil {
		writeAIError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, brief)
}

// Usage handles GET /ai/usage.
func (h *AIHandler) Usage(c *gin.Context) {
	user, ok := currentUserOr401(c)
	if !ok {
		return
	}
	usage, err := h.gateway.Usage(c.Request.Context(), user.ID)
	if err != nil {
		writeAIError(c, user.ID, err)
		return
	}
	c.JSON(http.StatusOK, usage)
}

// writeAIError maps AI and market errors to their status and domain.ErrorResponse. Provider details are
// logged with the user id only; prompts, answers and session data are never logged.
func writeAIError(c *gin.Context, userID uint, err error) {
	var validation *domain.ValidationError
	switch {
	case errors.As(err, &validation):
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: validation.Msg, Code: domain.CodeValidation})
	case errors.Is(err, domain.ErrInstrumentNotFound):
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: domain.ErrInstrumentNotFound.Error(), Code: domain.CodeInstrumentNotFound})
	case errors.Is(err, domain.ErrMarketDataUnavailable):
		log.Printf("[AI] market data failure for user_id=%d: %v", userID, err)
		c.JSON(http.StatusBadGateway, domain.ErrorResponse{Error: "market data unavailable", Code: domain.CodeMarketDataUnavailable})
	case errors.Is(err, domain.ErrAIBudgetExceeded):
		c.JSON(http.StatusTooManyRequests, domain.ErrorResponse{Error: domain.ErrAIBudgetExceeded.Error(), Code: domain.CodeAIBudgetExceeded})
	case errors.Is(err, domain.ErrAIRefused):
		log.Printf("[AI] model refused for user_id=%d", userID)
		c.JSON(http.StatusUnprocessableEntity, domain.ErrorResponse{Error: domain.ErrAIRefused.Error(), Code: domain.CodeAIRefused})
	case errors.Is(err, domain.ErrAIUnavailable):
		log.Printf("[AI] provider failure for user_id=%d: %v", userID, err)
		c.JSON(http.StatusServiceUnavailable, domain.ErrorResponse{Error: domain.ErrAIUnavailable.Error(), Code: domain.CodeAIUnavailable})
	default:
		log.Printf("[AI] internal error for user_id=%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
	}
}
