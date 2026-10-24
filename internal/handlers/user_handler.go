package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

type UserHandler struct {
	service *services.UserService
	auth    domain.AuthRepository
}

func NewUserHandler(service *services.UserService, auth domain.AuthRepository) *UserHandler {
	return &UserHandler{
		service: service,
		auth:    auth,
	}
}

func (h *UserHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/user/profile", middleware.RequireAuth(h.auth), h.Profile)
	v1.DELETE("/admin/users/:id", middleware.RequireAuth(h.auth), middleware.RequireRole(domain.RoleAdmin), h.DeleteUser)
}

// Profile returns the signed-in user's public profile (never the GORM model, so google_id stays private).
func (h *UserHandler) Profile(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}
	c.JSON(http.StatusOK, domain.NewProfileResponse(user))
}

func (h *UserHandler) DeleteUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "invalid user ID", Code: domain.CodeValidation})
		return
	}
	if err := h.service.DeleteUser(c.Request.Context(), uint(id)); err != nil {
		if errors.Is(err, domain.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, domain.ErrorResponse{Error: "user not found", Code: domain.CodeUserNotFound})
			return
		}
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "failed to delete user", Code: domain.CodeInternal})
		return
	}
	c.Status(http.StatusNoContent)
}
