package handlers

import (
	"crypto/subtle"
	"log"
	"net/http"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

const oauthStateCookie = "oauth_state"
const sessionCookie = "session_id"

// AuthHandler serves /auth/*. Its log lines carry only user IDs and outcomes: never session IDs,
// OAuth state values, cookie values, emails, or raw service errors (which may embed any of those).
type AuthHandler struct {
	service *services.AuthService
	cfg     *config.Config
}

func NewAuthHandler(service *services.AuthService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{service: service, cfg: cfg}
}

func (h *AuthHandler) Register(v1 *gin.RouterGroup) {
	auth := v1.Group("/auth")
	auth.GET("/login", h.Login)
	auth.GET("/callback", h.Callback)
	auth.POST("/logout", h.Logout)
	if h.cfg.Auth.GuestEnabled {
		auth.POST("/guest", middleware.RateLimitPerMinute(h.cfg.Auth.GuestRateLimitPerMinute), h.Guest)
	}
}

func (h *AuthHandler) Login(c *gin.Context) {
	url, state, err := h.service.LoginURL(c.Request.Context())
	if err != nil {
		log.Printf("[AUTH] login start failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start login"})
		return
	}
	h.setCookie(c, oauthStateCookie, state, 15*60)
	log.Printf("[AUTH] login started, redirecting to Google")
	c.Redirect(http.StatusFound, url)
}

func (h *AuthHandler) Callback(c *gin.Context) {
	queryState := c.Query("state")
	queryCode := c.Query("code")

	stateCookie, err := c.Cookie(oauthStateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(stateCookie), []byte(queryState)) != 1 {
		log.Printf("[AUTH] invalid OAuth state")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid OAuth state"})
		return
	}
	c.SetCookie(oauthStateCookie, "", -1, "/", "", h.cfg.Auth.CookieSecure, true)
	user, sessionID, err := h.service.Callback(c.Request.Context(), stateCookie, queryCode)
	if err != nil {
		log.Printf("[AUTH] Google login failed (has_code=%t)", queryCode != "")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Google authentication failed"})
		return
	}
	h.setCookie(c, sessionCookie, sessionID, int(h.cfg.Auth.SessionTTL.Seconds()))
	log.Printf("[AUTH] login succeeded user_id=%d", user.ID)
	c.Redirect(http.StatusFound, h.cfg.Auth.FrontendURL+"/dashboard")
}

// Guest creates a fresh guest account, signs it in and returns its profile (POST /auth/guest).
func (h *AuthHandler) Guest(c *gin.Context) {
	user, sessionID, err := h.service.GuestLogin(c.Request.Context())
	if err != nil {
		log.Printf("[AUTH] guest login failed")
		c.JSON(http.StatusInternalServerError, domain.ErrorResponse{Error: "failed to create guest", Code: domain.CodeInternal})
		return
	}
	h.setCookie(c, sessionCookie, sessionID, int(h.cfg.Auth.GuestSessionTTL.Seconds()))
	log.Printf("[AUTH] guest login succeeded user_id=%d", user.ID)
	c.JSON(http.StatusCreated, domain.NewProfileResponse(user))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	sessionID, err := c.Cookie(sessionCookie)
	if err == nil && sessionID != "" {
		if err := h.service.Logout(c.Request.Context(), sessionID); err != nil {
			log.Printf("[AUTH] logout failed to delete session")
		} else {
			log.Printf("[AUTH] logout succeeded")
		}
	} else {
		log.Printf("[AUTH] logout without session cookie")
	}

	c.SetCookie(sessionCookie, "", -1, "/", "", h.cfg.Auth.CookieSecure, true)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) setCookie(c *gin.Context, name, value string, maxAge int) {
	c.SetCookie(name, value, maxAge, "/", "", h.cfg.Auth.CookieSecure, true)
}
