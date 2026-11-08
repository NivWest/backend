package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/services"
	"backend/internal/testutil"
)

// Story NW-09: GET /market/stream. These tests run a real HTTP server (httptest.NewServer) with a short
// WriteTimeout, so the per-write deadline extension is exercised for real; the hub is a real services.QuoteHub
// on a QuoteBoard fake with manual tickers.

func streamQuote(id string, last float64) domain.Quote {
	return domain.Quote{
		OrderbookID: id, Symbol: strings.TrimPrefix(id, "SIM-"), Last: last,
		Bid: domain.RoundPrice(last * 0.999), Ask: domain.RoundPrice(last * 1.001),
		ChangePercent: 0.5, Currency: "SEK", UpdatedAt: fixedTime,
	}
}

type streamFixture struct {
	hub     *services.QuoteHub
	board   *testutil.QuoteBoard
	tickers *testutil.ManualTickers
	server  *httptest.Server
}

func newStreamFixture(t *testing.T, cfg StreamConfig) *streamFixture {
	t.Helper()
	f := &streamFixture{
		board:   testutil.NewQuoteBoard(streamQuote("SIM-FJRD", 100), streamQuote("SIM-NVE", 50)),
		tickers: &testutil.ManualTickers{},
	}
	f.hub = services.NewQuoteHub(f.board.Provider(), services.QuoteHubOptions{
		Interval:  time.Second,
		NewTicker: func(d time.Duration) services.Ticker { return f.tickers.New(d) },
		Logf:      func(string, ...any) {},
	})
	f.server = httptest.NewUnstartedServer(newEngine(NewMarketStreamHandler(f.hub, cfg)))
	f.server.Config.WriteTimeout = 300 * time.Millisecond // like SERVER_WRITE_TIMEOUT, but short
	f.server.Start()
	t.Cleanup(func() {
		f.hub.Close()
		f.server.Close()
	})
	return f
}

// get opens GET /api/v1/market/stream?<query> from its own client IP (X-Forwarded-For), so the shared per-IP
// /market rate limit never interferes. Close the body to disconnect.
func (f *streamFixture) get(t *testing.T, ctx context.Context, query, clientIP string) *http.Response {
	t.Helper()
	if clientIP == "" {
		n := marketClientSeq.Add(1)
		clientIP = fmt.Sprintf("10.%d.%d.%d", (n>>16)&0xff, (n>>8)&0xff, n&0xff)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/api/v1/market/stream?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", clientIP)
	res, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	return res
}

// sseFrame is one parsed event, or a comment (Comment set).
type sseFrame struct {
	Event, Data, Comment, Retry string
}

// frames parses the stream into frames on a channel, closed at EOF.
func frames(body io.Reader) <-chan sseFrame {
	out := make(chan sseFrame, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(body)
		var fr sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if fr != (sseFrame{}) {
					out <- fr
				}
				fr = sseFrame{}
			case strings.HasPrefix(line, ":"):
				fr.Comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			case strings.HasPrefix(line, "event: "):
				fr.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				fr.Data = strings.TrimPrefix(line, "data: ")
			case strings.HasPrefix(line, "retry: "):
				fr.Retry = strings.TrimPrefix(line, "retry: ")
			}
		}
	}()
	return out
}

func nextFrame(t *testing.T, ch <-chan sseFrame, within time.Duration) sseFrame {
	t.Helper()
	select {
	case fr, ok := <-ch:
		if !ok {
			t.Fatal("stream ended, want another frame")
		}
		return fr
	case <-time.After(within):
		t.Fatalf("no frame within %s", within)
	}
	return sseFrame{}
}

// nextQuote skips heartbeats and the retry frame and returns the next quote event.
func nextQuote(t *testing.T, ch <-chan sseFrame) domain.Quote {
	t.Helper()
	for {
		fr := nextFrame(t, ch, 2*time.Second)
		if fr.Comment == "ping" || fr.Retry != "" {
			continue
		}
		if fr.Event != "quote" {
			t.Fatalf("frame = %+v, want event quote", fr)
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(fr.Data), &raw); err != nil {
			t.Fatalf("quote data is not JSON: %v (%s)", err, fr.Data)
		}
		expectKeys(t, raw, "orderbook_id", "symbol", "last", "bid", "ask", "change_percent", "currency", "updated_at")
		var q domain.Quote
		if err := json.Unmarshal([]byte(fr.Data), &q); err != nil {
			t.Fatal(err)
		}
		return q
	}
}

func TestMarketStreamHandler_RejectsBeforeStreaming(t *testing.T) {
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = fmt.Sprintf("SIM-%02d", i)
	}
	tests := []struct {
		name   string
		query  string
		status int
		code   string
		setup  func(f *streamFixture)
	}{
		{name: "missing ids", query: "", status: http.StatusBadRequest, code: domain.CodeValidation},
		{name: "empty ids", query: "ids=", status: http.StatusBadRequest, code: domain.CodeValidation},
		{name: "only blank entries", query: "ids=%20,%20", status: http.StatusBadRequest, code: domain.CodeValidation},
		{name: "51 ids", query: "ids=" + strings.Join(ids, ","), status: http.StatusBadRequest, code: domain.CodeValidation},
		{name: "unknown id", query: "ids=SIM-FJRD,SIM-NOPE", status: http.StatusNotFound, code: domain.CodeInstrumentNotFound},
		{name: "upstream down", query: "ids=SIM-FJRD", status: http.StatusBadGateway, code: domain.CodeMarketDataUnavailable,
			setup: func(f *streamFixture) { f.board.Fail("SIM-FJRD", domain.ErrMarketDataUnavailable) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newStreamFixture(t, StreamConfig{})
			if tt.setup != nil {
				tt.setup(f)
			}
			res := f.get(t, context.Background(), tt.query, "")
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d (body %s)", res.StatusCode, tt.status, body)
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want a JSON error, not a stream", ct)
			}
			var e domain.ErrorResponse
			if err := json.Unmarshal(body, &e); err != nil || e.Code != tt.code || e.Error == "" {
				t.Errorf("body = %s, want {error, code %s}", body, tt.code)
			}
			if n := f.hub.Pollers(); n != 0 {
				t.Errorf("Pollers() = %d, want 0: nothing may be subscribed after a rejected request", n)
			}
		})
	}
}

func TestMarketStreamHandler_StreamsQuotes(t *testing.T) {
	f := newStreamFixture(t, StreamConfig{Heartbeat: time.Hour})
	res := f.get(t, context.Background(), "ids=SIM-FJRD,SIM-NVE,SIM-FJRD", "")
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache, no-transform",
		"X-Accel-Buffering": "no",
	} {
		if got := res.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	ch := frames(res.Body)
	if fr := nextFrame(t, ch, time.Second); fr.Retry != "3000" {
		t.Errorf("first frame = %+v, want retry: 3000", fr)
	}
	initial := map[string]float64{}
	for range 2 {
		q := nextQuote(t, ch)
		initial[q.OrderbookID] = q.Last
	}
	if len(initial) != 2 || initial["SIM-FJRD"] != 100 || initial["SIM-NVE"] != 50 {
		t.Errorf("initial quotes = %v, want one per distinct id (FJRD 100, NVE 50)", initial)
	}

	// A price change reaches the open stream on the next poll.
	f.board.Set(streamQuote("SIM-FJRD", 123.45))
	tickers := f.tickers.All()
	if len(tickers) != 2 {
		t.Fatalf("pollers = %d, want 2", len(tickers))
	}
	for _, tk := range tickers {
		if err := tk.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	q := nextQuote(t, ch)
	if q.OrderbookID != "SIM-FJRD" || q.Last != 123.45 || q.Ask != 123.57 {
		t.Errorf("pushed quote = %+v, want SIM-FJRD last 123.45 ask 123.57 (NVE unchanged, not sent)", q)
	}
}

func TestMarketStreamHandler_HeartbeatOutlivesWriteTimeout(t *testing.T) {
	f := newStreamFixture(t, StreamConfig{Heartbeat: 50 * time.Millisecond, WriteTimeout: 200 * time.Millisecond})
	res := f.get(t, context.Background(), "ids=SIM-FJRD", "")
	defer res.Body.Close()
	ch := frames(res.Body)

	// The server's WriteTimeout is 300ms; the stream must stay open (and keep pinging) for far longer.
	deadline := time.Now().Add(time.Second)
	pings := 0
	for time.Now().Before(deadline) {
		fr := nextFrame(t, ch, 200*time.Millisecond)
		if fr.Comment == "ping" {
			pings++
		}
	}
	if pings < 10 {
		t.Errorf("pings in 1s = %d, want about 20 (every 50ms) and the stream still open", pings)
	}
}

func TestMarketStreamHandler_DisconnectUnsubscribes(t *testing.T) {
	f := newStreamFixture(t, StreamConfig{Heartbeat: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	res := f.get(t, ctx, "ids=SIM-FJRD,SIM-NVE", "")
	ch := frames(res.Body)
	nextQuote(t, ch)
	if n := f.hub.Pollers(); n != 2 {
		t.Fatalf("Pollers() = %d, want 2 while streaming", n)
	}
	cancel()
	res.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for f.hub.Pollers() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("Pollers() = %d after the client left, want 0", f.hub.Pollers())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMarketStreamHandler_HubShutdownSendsErrorEvent(t *testing.T) {
	f := newStreamFixture(t, StreamConfig{Heartbeat: time.Hour})
	res := f.get(t, context.Background(), "ids=SIM-FJRD", "")
	defer res.Body.Close()
	ch := frames(res.Body)
	nextQuote(t, ch)

	f.hub.Close()
	fr := nextFrame(t, ch, time.Second)
	if fr.Event != "error" {
		t.Fatalf("frame after shutdown = %+v, want event error", fr)
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(fr.Data), &e); err != nil || e.Code != domain.CodeMarketDataUnavailable || e.Error == "" {
		t.Errorf("error data = %s, want an Error body with MARKET_DATA_UNAVAILABLE", fr.Data)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("stream continued after the error event")
		}
	case <-time.After(time.Second):
		t.Error("stream not closed after the error event")
	}
}

func TestMarketStreamHandler_CapsConcurrentStreamsPerIP(t *testing.T) {
	f := newStreamFixture(t, StreamConfig{Heartbeat: time.Hour, MaxPerIP: 2})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ip := "10.200.0.1"
	for range 2 {
		res := f.get(t, ctx, "ids=SIM-FJRD", ip)
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("stream status = %d, want 200", res.StatusCode)
		}
	}
	res := f.get(t, ctx, "ids=SIM-FJRD", ip)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(string(body), domain.CodeRateLimited) {
		t.Errorf("third stream = %d %s, want 429 RATE_LIMITED", res.StatusCode, body)
	}
	other := f.get(t, ctx, "ids=SIM-FJRD", "10.200.0.2")
	defer other.Body.Close()
	if other.StatusCode != http.StatusOK {
		t.Errorf("another IP's stream = %d, want 200", other.StatusCode)
	}
}
