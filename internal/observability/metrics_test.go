package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func TestMetrics_HTTPMetricsAndPrometheusOutput(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	engine := gin.New()
	engine.Use(HTTPMetrics(m))
	engine.GET("/api/v1/market/instruments", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	engine.GET("/api/v1/market/instruments/:orderbook_id", func(c *gin.Context) {
		c.String(http.StatusOK, c.Param("orderbook_id"))
	})
	engine.GET("/metrics", gin.WrapH(m.Handler()))

	// 1. Call /api/v1/market/instruments
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/market/instruments", nil)
	engine.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w1.Code)
	}

	// 2. Call path with parameter to verify route label uses template, not raw id
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/market/instruments/SIM-FJRD", nil)
	engine.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w2.Code)
	}

	// 3. Call /metrics
	wMetrics := httptest.NewRecorder()
	reqMetrics := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	engine.ServeHTTP(wMetrics, reqMetrics)

	if wMetrics.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", wMetrics.Code)
	}

	body := wMetrics.Body.String()

	// Verify AC2 requirements:
	// - http_request_duration_seconds_bucket with labels route="/api/v1/market/instruments", method="GET", status="200"
	if !strings.Contains(body, `http_request_duration_seconds_bucket{method="GET",route="/api/v1/market/instruments",status="200"`) {
		t.Errorf("metrics missing expected http_request_duration_seconds_bucket for /api/v1/market/instruments: %s", body)
	}

	// - Verify the route label is template (/api/v1/market/instruments/:orderbook_id), not SIM-FJRD
	if strings.Contains(body, "SIM-FJRD") {
		t.Errorf("metrics contains raw instrument ID SIM-FJRD; route label must be template: %s", body)
	}
	if !strings.Contains(body, `route="/api/v1/market/instruments/:orderbook_id"`) {
		t.Errorf("metrics missing template route label /api/v1/market/instruments/:orderbook_id: %s", body)
	}

	// - http_requests_in_flight
	if !strings.Contains(body, "http_requests_in_flight") {
		t.Errorf("metrics missing http_requests_in_flight: %s", body)
	}

	// - go_goroutines
	if !strings.Contains(body, "go_goroutines") {
		t.Errorf("metrics missing go_goroutines: %s", body)
	}
}

func TestMetrics_DomainObservers(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Test ObserveOrderFilled
	m.ObserveOrderFilled("BUY", "MARKET")
	m.ObserveOrderFilled("SELL", "MARKET")

	// Test ObserveLLMCall
	m.ObserveLLMCall("instrument_brief", "fake", "ok", 150*time.Millisecond, 250, 50, 0.0)

	// Test SetSubscribers
	m.SetSubscribers(4)

	handler := m.Handler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)

	respBody, _ := io.ReadAll(rec.Body)
	body := string(respBody)

	// Check orders_filled_total{side="BUY",type="MARKET"}
	if !strings.Contains(body, `orders_filled_total{side="BUY",type="MARKET"} 1`) {
		t.Errorf("metrics missing orders_filled_total for BUY MARKET: %s", body)
	}

	// Check llm_requests_total{feature="instrument_brief",provider="fake",status="ok"}
	if !strings.Contains(body, `llm_requests_total{feature="instrument_brief",provider="fake",status="ok"} 1`) {
		t.Errorf("metrics missing llm_requests_total: %s", body)
	}

	// Check llm_tokens_total{feature="instrument_brief",kind="output"} 50
	if !strings.Contains(body, `llm_tokens_total{feature="instrument_brief",kind="output"} 50`) {
		t.Errorf("metrics missing llm_tokens_total output: %s", body)
	}
	if !strings.Contains(body, `llm_tokens_total{feature="instrument_brief",kind="input"} 250`) {
		t.Errorf("metrics missing llm_tokens_total input: %s", body)
	}

	// Check quote_hub_subscribers 4
	if !strings.Contains(body, "quote_hub_subscribers 4") {
		t.Errorf("metrics missing quote_hub_subscribers: %s", body)
	}
}
