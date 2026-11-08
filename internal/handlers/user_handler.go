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
	v1.PATCH("/me", middleware.RequireAuth(h.auth), h.UpdateProfile)
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

// UpdateProfile updates the signed-in user's display name and/or leaderboard opt-in status (story NW-13).
func (h *UserHandler) UpdateProfile(c *gin.Context) {
	user, ok := middleware.CurrentUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated})
		return
	}

	var req domain.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.ErrorResponse{Error: "invalid request body", Code: domain.CodeValidation})
		return
	}

	profile, err := h.service.UpdateProfile(c.Request.Context(), user.ID, req)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidDisplayName) {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{
				Error: "display name must be 3-20 characters [A-Za-z0-9_-]",
				Code:  domain.CodeValidation,
			})
			return
		}
		if errors.Is(err, domain.ErrDisplayNameRequired) {
			c.JSON(http.StatusBadRequest, domain.ErrorResponse{
				Error: "display name is required to join the leaderboard",
				Code:  domain.CodeValidation,
			})
			return
		}
		if errors.Is(err, domain.ErrDisplayNameTaken) {
			c.JSON(http.StatusConflict, domain.ErrorResponse{
				Error: "display name already taken",
				Code:  domain.CodeConflict,
			})
			return
		}
		if errors.Is(err, domain.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, domain.ErrorResponse{
				Error: "user not found",
				Code:  domain.CodeUserNotFound,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{
			Error: "failed to update profile",
			Code:  domain.CodeInternal,
		})
		return
	}

	c.JSON(http.StatusOK, profile)
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
