package observability

import (
	"net/http"
	"strconv"
	"time"

	"backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics encapsulates all Prometheus metrics collectors and the registry.
type Metrics struct {
	Registry *prometheus.Registry

	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	ordersFilled        *prometheus.CounterVec
	llmRequests         *prometheus.CounterVec
	llmTokens           *prometheus.CounterVec
	llmLatency          *prometheus.HistogramVec
	aiCost              *prometheus.CounterVec
	quoteHubSubscribers prometheus.Gauge
	matchingCycle       prometheus.Histogram
	marketRequests      *prometheus.CounterVec
	marketLatency       *prometheus.HistogramVec
}

var _ domain.OrderMetricsObserver = (*Metrics)(nil)
var _ domain.AIMetricsObserver = (*Metrics)(nil)
var _ domain.QuoteHubMetricsObserver = (*Metrics)(nil)

// NewMetrics initializes Prometheus collectors and registers them with the given registerer.
// If reg is nil or a *prometheus.Registry, a registry is created/used and collectors.NewGoCollector() is added.
func NewMetrics(reg *prometheus.Registry) *Metrics {
	if reg == nil {
		reg = prometheus.NewRegistry()
	}

	m := &Metrics{
		Registry: reg,

		httpDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds.",
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"route", "method", "status"},
		),

		httpInFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "http_requests_in_flight",
				Help: "Current number of HTTP requests being served.",
			},
		),

		ordersFilled: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "orders_filled_total",
				Help: "Total number of filled orders.",
			},
			[]string{"side", "type"},
		),

		llmRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_requests_total",
				Help: "Total number of LLM operations.",
			},
			[]string{"feature", "provider", "status"},
		),

		llmTokens: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_tokens_total",
				Help: "Total number of LLM tokens processed.",
			},
			[]string{"feature", "kind"},
		),

		llmLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_request_duration_seconds",
				Help:    "Latency of LLM calls in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"feature", "provider"},
		),

		aiCost: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ai_cost_usd_total",
				Help: "Total estimated AI cost in USD.",
			},
			[]string{"feature", "provider"},
		),

		quoteHubSubscribers: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "quote_hub_subscribers",
				Help: "Current count of active quote stream subscribers.",
			},
		),

		matchingCycle: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "matching_engine_cycle_seconds",
				Help:    "Duration of matching engine run cycles.",
				Buckets: prometheus.DefBuckets,
			},
		),

		marketRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "market_data_requests_total",
				Help: "Total market data upstream requests.",
			},
			[]string{"provider", "outcome"},
		),

		marketLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "market_data_request_duration_seconds",
				Help:    "Latency of upstream market data requests.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"provider"},
		),
	}

	// Standard Go metrics (including go_goroutines required by AC2)
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Register all custom metrics
	reg.MustRegister(
		m.httpDuration,
		m.httpInFlight,
		m.ordersFilled,
		m.llmRequests,
		m.llmTokens,
		m.llmLatency,
		m.aiCost,
		m.quoteHubSubscribers,
		m.matchingCycle,
		m.marketRequests,
		m.marketLatency,
	)

	return m
}

// Handler returns an http.Handler that serves the Prometheus metrics text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

// ObserveOrderFilled records a filled order with side (BUY/SELL) and type (MARKET/etc.).
func (m *Metrics) ObserveOrderFilled(side, orderType string) {
	if m != nil && m.ordersFilled != nil {
		m.ordersFilled.WithLabelValues(side, orderType).Inc()
	}
}

// ObserveLLMCall records an LLM request, its tokens, latency, and cost.
func (m *Metrics) ObserveLLMCall(feature, provider, status string, latency time.Duration, inputTokens, outputTokens int64, costUSD float64) {
	if m == nil {
		return
	}
	if m.llmRequests != nil {
		m.llmRequests.WithLabelValues(feature, provider, status).Inc()
	}
	if m.llmTokens != nil {
		if inputTokens > 0 {
			m.llmTokens.WithLabelValues(feature, "input").Add(float64(inputTokens))
		}
		if outputTokens > 0 {
			m.llmTokens.WithLabelValues(feature, "output").Add(float64(outputTokens))
		}
	}
	if m.llmLatency != nil && latency > 0 {
		m.llmLatency.WithLabelValues(feature, provider).Observe(latency.Seconds())
	}
	if m.aiCost != nil && costUSD > 0 {
		m.aiCost.WithLabelValues(feature, provider).Add(costUSD)
	}
}

// SetSubscribers updates the quote hub subscribers gauge.
func (m *Metrics) SetSubscribers(count int) {
	if m != nil && m.quoteHubSubscribers != nil {
		m.quoteHubSubscribers.Set(float64(count))
	}
}

// HTTPMetrics returns a Gin middleware that records HTTP request duration and in-flight counts.
// The route label uses the Gin full path template (e.g. "/api/v1/market/instruments/:orderbook_id"),
// never raw IDs, keeping cardinality bounded (AC2).
func HTTPMetrics(m *Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if m == nil {
			c.Next()
			return
		}

		m.httpInFlight.Inc()
		defer m.httpInFlight.Dec()

		t0 := time.Now()
		c.Next()
		duration := time.Since(t0).Seconds()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		status := strconv.Itoa(c.Writer.Status())
		method := c.Request.Method
		m.httpDuration.WithLabelValues(route, method, status).Observe(duration)
	}
}
