package middleware

import (
	"net/http"

	"backend/internal/domain"
	"backend/internal/models"
	"github.com/gin-gonic/gin"
)

const userContextKey = "authenticated_user"

func RequireAuth(auth domain.AuthRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := c.Cookie("session_id")
		if err != nil || sessionID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		user, err := auth.GetSessionUser(c.Request.Context(), sessionID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired session"})
			return
		}
		c.Set(userContextKey, user)
		c.Next()
	}
}

func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, exists := c.Get(userContextKey)
		user, ok := value.(*models.User)
		if !exists || !ok || user.Role != role {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Next()
	}
}

func CurrentUser(c *gin.Context) (*models.User, bool) {
	value, exists := c.Get(userContextKey)
	user, ok := value.(*models.User)
	return user, exists && ok
}
