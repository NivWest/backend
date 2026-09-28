package handlers

import (
	"crypto/subtle"
	"log"
	"net/http"

	"backend/internal/config"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

const oauthStateCookie = "oauth_state"
const sessionCookie = "session_id"

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
}

func (h *AuthHandler) Login(c *gin.Context) {
	log.Printf("[AUTH] Starting OAuth login flow from %s", c.ClientIP())
	url, state, err := h.service.LoginURL(c.Request.Context())
	if err != nil {
		log.Printf("[AUTH] Failed to generate login URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start login"})
		return
	}
	h.setCookie(c, oauthStateCookie, state, 15*60)
	log.Printf("[AUTH] OAuth state set in cookie (%s), redirecting to Google OAuth", state)
	c.Redirect(http.StatusFound, url)
}

func (h *AuthHandler) Callback(c *gin.Context) {
	queryState := c.Query("state")
	queryCode := c.Query("code")
	log.Printf("[AUTH] OAuth callback received (state=%s, has_code=%t)", queryState, queryCode != "")

	stateCookie, err := c.Cookie(oauthStateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(stateCookie), []byte(queryState)) != 1 {
		log.Printf("[AUTH] Invalid OAuth state: cookie=%q, query=%q, err=%v", stateCookie, queryState, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid OAuth state"})
		return
	}
	c.SetCookie(oauthStateCookie, "", -1, "/", "", h.cfg.Auth.CookieSecure, true)
	user, sessionID, err := h.service.Callback(c.Request.Context(), stateCookie, queryCode)
	if err != nil {
		log.Printf("[AUTH] Google authentication callback failed: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Google authentication failed"})
		return
	}
	h.setCookie(c, sessionCookie, sessionID, int(h.cfg.Auth.SessionTTL.Seconds()))
	log.Printf("[AUTH] Authentication successful: user_id=%d, email=%s, session_id=%s. Redirecting to %s/dashboard", user.ID, user.Email, sessionID, h.cfg.Auth.FrontendURL)
	c.Redirect(http.StatusFound, h.cfg.Auth.FrontendURL+"/dashboard")
}

func (h *AuthHandler) Logout(c *gin.Context) {
	sessionID, err := c.Cookie(sessionCookie)
	if err == nil && sessionID != "" {
		log.Printf("[AUTH] Logging out session_id=%s", sessionID)
		if err := h.service.Logout(c.Request.Context(), sessionID); err != nil {
			log.Printf("[AUTH] Error deleting session %s: %v", sessionID, err)
		} else {
			log.Printf("[AUTH] Session %s invalidated in database", sessionID)
		}
	} else {
		log.Printf("[AUTH] Logout called without active session cookie")
	}

	c.SetCookie(sessionCookie, "", -1, "/", "", h.cfg.Auth.CookieSecure, true)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) setCookie(c *gin.Context, name, value string, maxAge int) {
	c.SetCookie(name, value, maxAge, "/", "", h.cfg.Auth.CookieSecure, true)
}
