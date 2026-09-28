package middleware

import (
	"log"
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
			log.Printf("[AUTH-MW] %s %s - Authentication required: missing or empty session_id cookie (err=%v)", c.Request.Method, c.Request.URL.Path, err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		user, err := auth.GetSessionUser(c.Request.Context(), sessionID)
		if err != nil {
			log.Printf("[AUTH-MW] %s %s - Session validation failed for session_id=%s: %v", c.Request.Method, c.Request.URL.Path, sessionID, err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired session"})
			return
		}
		log.Printf("[AUTH-MW] %s %s - User authenticated: ID=%d, email=%s, role=%s (session_id=%s)", c.Request.Method, c.Request.URL.Path, user.ID, user.Email, user.Role, sessionID)
		c.Set(userContextKey, user)
		c.Next()
	}
}

func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, exists := c.Get(userContextKey)
		user, ok := value.(*models.User)
		if !exists || !ok {
			log.Printf("[AUTH-MW] %s %s - Role check failed: user context missing (required role=%s)", c.Request.Method, c.Request.URL.Path, role)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if user.Role != role {
			log.Printf("[AUTH-MW] %s %s - Forbidden: user_id=%d role=%s does not match required role=%s", c.Request.Method, c.Request.URL.Path, user.ID, user.Role, role)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		log.Printf("[AUTH-MW] %s %s - Role authorized: user_id=%d, role=%s", c.Request.Method, c.Request.URL.Path, user.ID, user.Role)
		c.Next()
	}
}

func CurrentUser(c *gin.Context) (*models.User, bool) {
	value, exists := c.Get(userContextKey)
	user, ok := value.(*models.User)
	return user, exists && ok
}
