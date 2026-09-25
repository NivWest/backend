package handlers

import (
	"crypto/subtle"
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
	url, state, err := h.service.LoginURL(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start login"})
		return
	}
	h.setCookie(c, oauthStateCookie, state, 15*60)
	c.Redirect(http.StatusFound, url)
}

func (h *AuthHandler) Callback(c *gin.Context) {
	stateCookie, err := c.Cookie(oauthStateCookie)
	if err != nil || subtle.ConstantTimeCompare([]byte(stateCookie), []byte(c.Query("state"))) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid OAuth state"})
		return
	}
	c.SetCookie(oauthStateCookie, "", -1, "/", "", h.cfg.Auth.CookieSecure, true)
	_, sessionID, err := h.service.Callback(c.Request.Context(), stateCookie, c.Query("code"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Google authentication failed"})
		return
	}
	h.setCookie(c, sessionCookie, sessionID, int(h.cfg.Auth.SessionTTL.Seconds()))
	c.Redirect(http.StatusFound, h.cfg.Auth.FrontendURL+"/dashboard")
}

func (h *AuthHandler) Logout(c *gin.Context) {
	if sessionID, err := c.Cookie(sessionCookie); err == nil {
		_ = h.service.Logout(c.Request.Context(), sessionID)
	}
	c.SetCookie(sessionCookie, "", -1, "/", "", h.cfg.Auth.CookieSecure, true)
	c.Status(http.StatusNoContent)
}

func (h *AuthHandler) setCookie(c *gin.Context, name, value string, maxAge int) {
	c.SetCookie(name, value, maxAge, "/", "", h.cfg.Auth.CookieSecure, true)
}
