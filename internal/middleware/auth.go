package middleware

import (
	"log"
	"net/http"

	"backend/internal/domain"
	"backend/internal/models"
	"github.com/gin-gonic/gin"
)

const userContextKey = "authenticated_user"

var (
	errUnauthenticated = domain.ErrorResponse{Error: "authentication required", Code: domain.CodeUnauthenticated}
	errForbidden       = domain.ErrorResponse{Error: "forbidden", Code: domain.CodeForbidden}
)

// RequireAuth resolves the session_id cookie to a user and stores it in the context.
// A missing cookie and an unknown or expired session both abort with 401 UNAUTHENTICATED.
//
// Logs never contain the session ID, the user's email, or the repository error
// (which may echo the session ID back from the query).
func RequireAuth(auth domain.AuthRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := c.Cookie("session_id")
		if err != nil || sessionID == "" {
			log.Printf("[AUTH-MW] %s %s - missing session cookie", c.Request.Method, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, errUnauthenticated)
			return
		}
		user, err := auth.GetSessionUser(c.Request.Context(), sessionID)
		if err != nil || user == nil {
			log.Printf("[AUTH-MW] %s %s - invalid or expired session", c.Request.Method, c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, errUnauthenticated)
			return
		}
		c.Set(userContextKey, user)
		c.Next()
	}
}

// RequireRole aborts with 403 FORBIDDEN unless RequireAuth stored a user with the given role.
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok {
			log.Printf("[AUTH-MW] %s %s - forbidden: no authenticated user (required role=%s)", c.Request.Method, c.Request.URL.Path, role)
			c.AbortWithStatusJSON(http.StatusForbidden, errForbidden)
			return
		}
		if user.Role != role {
			log.Printf("[AUTH-MW] %s %s - forbidden: user_id=%d role=%s (required role=%s)", c.Request.Method, c.Request.URL.Path, user.ID, user.Role, role)
			c.AbortWithStatusJSON(http.StatusForbidden, errForbidden)
			return
		}
		c.Next()
	}
}

// CurrentUser returns the user stored by RequireAuth.
func CurrentUser(c *gin.Context) (*models.User, bool) {
	value, exists := c.Get(userContextKey)
	user, ok := value.(*models.User)
	return user, exists && ok && user != nil
}
