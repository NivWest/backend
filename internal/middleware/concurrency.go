package middleware

import (
	"net/http"
	"sync"

	"backend/internal/domain"
	"github.com/gin-gonic/gin"
)

var errTooManyStreams = domain.ErrorResponse{Error: "too many open streams from this address", Code: domain.CodeRateLimited}

// ConcurrentLimit lets each client IP run at most maxPerIP requests through the route at the same time; one more
// is answered 429 RATE_LIMITED. A slot is held until the handler returns, which for a stream is when it ends.
// Every call owns its own counters. A maxPerIP below 1 is treated as 1.
func ConcurrentLimit(maxPerIP int) gin.HandlerFunc {
	if maxPerIP < 1 {
		maxPerIP = 1
	}
	var mu sync.Mutex
	open := map[string]int{}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		mu.Lock()
		if open[ip] >= maxPerIP {
			mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, errTooManyStreams)
			return
		}
		open[ip]++
		mu.Unlock()
		defer func() {
			mu.Lock()
			defer mu.Unlock()
			if open[ip]--; open[ip] <= 0 {
				delete(open, ip)
			}
		}()
		c.Next()
	}
}
