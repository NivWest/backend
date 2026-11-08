package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"backend/internal/domain"
	"backend/internal/middleware"
	"backend/internal/services"
	"github.com/gin-gonic/gin"
)

// Live quotes over Server-Sent Events (story NW-09): GET /market/stream?ids=.

// Stream defaults, used for zero StreamConfig fields.
const (
	DefaultStreamHeartbeat    = 10 * time.Second
	DefaultStreamWriteTimeout = 10 * time.Second
	DefaultStreamMaxPerIP     = 5
	// streamRetry is the reconnect delay the stream suggests to EventSource clients (`retry:`).
	streamRetry = 3 * time.Second
)

// StreamConfig tunes MarketStreamHandler.
type StreamConfig struct {
	// Heartbeat is the interval of `: ping` comments (MARKET_STREAM_HEARTBEAT).
	Heartbeat time.Duration
	// WriteTimeout bounds each single write. The server's global SERVER_WRITE_TIMEOUT would cut every stream after
	// 10 s, so the handler moves the connection's write deadline forward before each write instead.
	WriteTimeout time.Duration
	// MaxPerIP caps the open streams of one client IP (MARKET_STREAM_MAX_PER_IP).
	MaxPerIP int
}

// MarketStreamHandler serves GET /market/stream. It is public, counts as one request against the shared /market
// rate limit at connect, and holds one of the IP's MaxPerIP stream slots until the stream ends.
type MarketStreamHandler struct {
	hub domain.QuoteHub
	cfg StreamConfig
}

func NewMarketStreamHandler(hub domain.QuoteHub, cfg StreamConfig) *MarketStreamHandler {
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = DefaultStreamHeartbeat
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = DefaultStreamWriteTimeout
	}
	if cfg.MaxPerIP <= 0 {
		cfg.MaxPerIP = DefaultStreamMaxPerIP
	}
	return &MarketStreamHandler{hub: hub, cfg: cfg}
}

func (h *MarketStreamHandler) Register(v1 *gin.RouterGroup) {
	v1.GET("/market/stream", middleware.RateLimit(), middleware.ConcurrentLimit(h.cfg.MaxPerIP), h.Stream)
}

var streamClosedEvent = mustJSON(domain.ErrorResponse{
	Error: "the quote stream was closed by the server, reconnect",
	Code:  domain.CodeMarketDataUnavailable,
})

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Stream handles GET /market/stream?ids=. Every validation and id lookup happens before the first byte of the
// stream, so errors are ordinary JSON responses (400 VALIDATION_ERROR, 404 INSTRUMENT_NOT_FOUND, 502).
func (h *MarketStreamHandler) Stream(c *gin.Context) {
	ids, err := services.ParseStreamIDs(c.Query("ids"))
	if err != nil {
		writeMarketError(c, err)
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel() // unsubscribes (the hub watches ctx)

	quotes, err := h.hub.Subscribe(ctx, ids)
	if err != nil {
		writeMarketError(c, err)
		return
	}

	header := c.Writer.Header()
	header.Set("Content-Type", "text/event-stream")
	// no-transform keeps compressing proxies (e.g. the Next.js server) from buffering the stream.
	header.Set("Cache-Control", "no-cache, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	rc := http.NewResponseController(c.Writer)
	write := func(chunk []byte) error {
		// Not supported by every ResponseWriter (e.g. test recorders); then there is no deadline to move either.
		if err := rc.SetWriteDeadline(time.Now().Add(h.cfg.WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if _, err := c.Writer.Write(chunk); err != nil {
			return err
		}
		return rc.Flush()
	}

	if err := write([]byte("retry: " + strconv.FormatInt(streamRetry.Milliseconds(), 10) + "\n\n")); err != nil {
		return
	}
	heartbeat := time.NewTicker(h.cfg.Heartbeat)
	defer heartbeat.Stop()
	for {
		var chunk []byte
		select {
		case <-ctx.Done():
			return
		case q, ok := <-quotes:
			if !ok {
				// The hub shut down (server shutdown): tell the client, then end the stream.
				_ = write(sseEvent("error", streamClosedEvent))
				return
			}
			data, err := json.Marshal(q)
			if err != nil {
				log.Printf("[STREAM] encode quote %s: %v", q.OrderbookID, err)
				continue
			}
			chunk = sseEvent("quote", data)
		case <-heartbeat.C:
			chunk = []byte(": ping\n\n")
		}
		if err := write(chunk); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, io.ErrClosedPipe) {
				log.Printf("[STREAM] write failed, closing the stream: %v", err)
			}
			return
		}
	}
}

// sseEvent formats one Server-Sent Event. data is single-line JSON, so one data line suffices.
func sseEvent(name string, data []byte) []byte {
	out := make([]byte, 0, len(name)+len(data)+16)
	out = append(out, "event: "...)
	out = append(out, name...)
	out = append(out, "\ndata: "...)
	out = append(out, data...)
	out = append(out, "\n\n"...)
	return out
}
