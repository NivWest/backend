package middleware

import (
	"net/http"
	"sync"
	"time"

	"backend/internal/domain"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

var errRateLimited = domain.ErrorResponse{Error: "too many requests, please try again later", Code: domain.CodeRateLimited}

// ipLimiter keeps one token bucket per client IP.
type ipLimiter struct {
	mu       sync.Mutex
	visitors map[string]*rate.Limiter
	limit    rate.Limit
	burst    int
}

func newIPLimiter(limit rate.Limit, burst int) *ipLimiter {
	return &ipLimiter{visitors: make(map[string]*rate.Limiter), limit: limit, burst: burst}
}

func (l *ipLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	limiter, exists := l.visitors[ip]
	if !exists {
		limiter = rate.NewLimiter(l.limit, l.burst)
		l.visitors[ip] = limiter
	}
	return limiter
}

func (l *ipLimiter) handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.get(c.ClientIP()).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, errRateLimited)
			return
		}
		c.Next()
	}
}

// defaultLimiter backs RateLimit: 5 requests per second, burst 10, shared by every RateLimit() call.
var defaultLimiter = newIPLimiter(rate.Every(time.Second/5), 10)

// RateLimit limits each client IP to 5 requests per second with a burst of 10.
func RateLimit() gin.HandlerFunc {
	return defaultLimiter.handler()
}

// RateLimitPerMinute limits each client IP to perMinute requests per minute, with a burst of perMinute.
// Every call owns its own buckets, so two routes using it never share a budget.
// A perMinute below 1 is treated as 1.
func RateLimitPerMinute(perMinute int) gin.HandlerFunc {
	if perMinute < 1 {
		perMinute = 1
	}
	return newIPLimiter(rate.Every(time.Minute/time.Duration(perMinute)), perMinute).handler()
}
