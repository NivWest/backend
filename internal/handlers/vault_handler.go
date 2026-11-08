package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
)

type VaultHandler struct {
	vault *services.VaultService
	auth  domain.AuthRepository
}

func NewVaultHandler(vault *services.VaultService, auth domain.AuthRepository) *VaultHandler {
	return &VaultHandler{vault: vault, auth: auth}
}

func (h *VaultHandler) Register(v1 *gin.RouterGroup) {
	vault := v1.Group("/vault")
	vault.Use(middleware.RequireAuth(h.auth))
	{
		vault.GET("", h.GetVault)
		vault.POST("/deposits", h.Deposit)
		vault.POST("/withdrawals", h.Withdraw)
		vault.GET("/transactions", h.ListTransactions)
	}
}

func (h *VaultHandler) GetVault(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	v, err := h.vault.GetVault(c.Request.Context(), user.ID)
	if err != nil {
		writeVaultError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *VaultHandler) Deposit(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	amount, err := decodeTransferRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: err.Error(), Code: domain.CodeValidation})
		return
	}

	tx, err := h.vault.Deposit(c.Request.Context(), user.ID, amount)
	if err != nil {
		writeVaultError(c, err)
		return
	}

	c.JSON(http.StatusCreated, tx)
}

func (h *VaultHandler) Withdraw(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	amount, err := decodeTransferRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: err.Error(), Code: domain.CodeValidation})
		return
	}

	tx, err := h.vault.Withdraw(c.Request.Context(), user.ID, amount)
	if err != nil {
		writeVaultError(c, err)
		return
	}

	c.JSON(http.StatusCreated, tx)
}

func (h *VaultHandler) ListTransactions(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	res, err := h.vault.ListTransactions(c.Request.Context(), user.ID)
	if err != nil {
		writeVaultError(c, err)
		return
	}

	c.JSON(http.StatusOK, res)
}

func decodeTransferRequest(c *gin.Context) (decimal.Decimal, error) {
	var body struct {
		Amount *decimal.Decimal `json:"amount"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	if err != nil {
		return decimal.Zero, errors.New("request body is too large or unreadable")
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Amount == nil {
		return decimal.Zero, errors.New("amount is required and must be a number")
	}
	return *body.Amount, nil
}

func writeVaultError(c *gin.Context, err error) {
	var valErr *domain.ValidationError
	if errors.As(err, &valErr) {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: valErr.Msg, Code: domain.CodeValidation})
		return
	}
	if errors.Is(err, domain.ErrInsufficientFunds) {
		c.JSON(http.StatusUnprocessableEntity, domain.ErrorResponse{Error: "insufficient buying power", Code: domain.CodeInsufficientFunds})
		return
	}
	if errors.Is(err, domain.ErrInsufficientVaultBalance) {
		c.JSON(http.StatusUnprocessableEntity, domain.ErrorResponse{Error: "insufficient vault balance", Code: domain.CodeInsufficientVaultBalance})
		return
	}
	if errors.Is(err, domain.ErrUserNotFound) {
		c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: "user not found", Code: domain.CodeUserNotFound})
		return
	}
	c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "internal server error", Code: domain.CodeInternal})
}
