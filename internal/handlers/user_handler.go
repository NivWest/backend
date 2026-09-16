package handlers

import (
	"net/http"
	"strconv"
	"errors"
	"github.com/gin-gonic/gin"
	"backend/internal/domain"
	"backend/internal/services"
	"backend/internal/models"
)

type UserHandler struct {
	service *services.UserService
}

func NewUserHandler(service *services.UserService) *UserHandler{
	return &UserHandler{
		service: service,
	}
}

func (h *UserHandler) Register(v1 *gin.RouterGroup) {
	users := v1.Group("/users")

	users.GET("/:id", h.GetUserByID)
	users.POST("/", h.CreateUser)
}

func (h *UserHandler) GetUserByID(c *gin.Context){
	id := c.Param("id")
	uid, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid user ID",
		})
		return
	}
	user, err := h.service.GetUserByID(c.Request.Context(), uint(uid))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *UserHandler) CreateUser(c *gin.Context) {
    var user models.User

    if err := c.ShouldBindJSON(&user); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "invalid request body",
        })
        return
    }

    if err := h.service.CreateUser(c.Request.Context(), &user); err != nil {
        if errors.Is(err, domain.ErrUserAlreadyExists) {
            c.JSON(http.StatusConflict, gin.H{
                "error": "user already exists",
            })
            return
        }

        c.JSON(http.StatusInternalServerError, gin.H{
            "error": "failed to create user",
        })
        return
    }

    c.JSON(http.StatusCreated, user)
}