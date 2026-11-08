package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"
	"github.com/gin-gonic/gin"
)

// Story NW-09: at most N concurrent requests (streams) per client IP.
func TestConcurrentLimit(t *testing.T) {
	var mu sync.Mutex
	gates := map[string]chan struct{}{} // client IP → closed to let its held requests finish
	gate := func(ip string) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		if gates[ip] == nil {
			gates[ip] = make(chan struct{})
		}
		return gates[ip]
	}
	entered := make(chan string, 16)

	r := gin.New()
	r.GET("/", ConcurrentLimit(2), func(c *gin.Context) {
		entered <- c.ClientIP()
		<-gate(c.ClientIP())
		c.Status(http.StatusNoContent)
	})
	do := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	hold := func(ip string, n int) <-chan int {
		t.Helper()
		codes := make(chan int, n)
		for range n {
			go func() { codes <- do(ip).Code }()
		}
		for range n {
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("handler not entered")
			}
		}
		return codes
	}
	expectCodes := func(codes <-chan int, n, want int) {
		t.Helper()
		for range n {
			select {
			case code := <-codes:
				if code != want {
					t.Errorf("held request status = %d, want %d", code, want)
				}
			case <-time.After(time.Second):
				t.Fatal("held request did not finish")
			}
		}
	}

	held := hold("10.0.0.1", 2)

	tests := []struct {
		name   string
		ip     string
		reject bool
	}{
		{name: "a third concurrent request from the same IP is 429", ip: "10.0.0.1", reject: true},
		{name: "another IP has its own budget", ip: "10.0.0.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.reject {
				assertJSONError(t, do(tt.ip), http.StatusTooManyRequests, domain.CodeRateLimited)
				return
			}
			codes := hold(tt.ip, 1)
			close(gate(tt.ip))
			expectCodes(codes, 1, http.StatusNoContent)
		})
	}

	close(gate("10.0.0.1"))
	expectCodes(held, 2, http.StatusNoContent)

	t.Run("slots are released when the handler returns", func(t *testing.T) {
		for range 3 {
			if w := do("10.0.0.1"); w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 once the earlier requests finished", w.Code)
			}
			<-entered
		}
	})
}
