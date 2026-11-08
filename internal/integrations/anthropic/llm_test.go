package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// replay serves one recorded fixture with the given status and captures the request it received.
type replay struct {
	mu      sync.Mutex
	body    map[string]any
	headers http.Header
	path    string
}

func newReplay(t *testing.T, status int, fixture, contentType string) (*replay, *httptest.Server) {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	rp := &replay{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rp.mu.Lock()
		rp.path = r.URL.Path
		rp.headers = r.Header.Clone()
		_ = json.Unmarshal(raw, &rp.body)
		rp.mu.Unlock()
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return rp, srv
}

func newTestLLM(srv *httptest.Server) *LLM {
	return New("test-key", "claude-opus-5-5", srv.URL, option.WithMaxRetries(0))
}

var briefSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"summary"},
	"properties":           map[string]any{"summary": map[string]any{"type": "string"}},
}

func briefReq() domain.LLMRequest {
	return domain.LLMRequest{
		Feature:       domain.AIFeatureInstrumentBrief,
		PromptVersion: "instrument_brief.v1",
		System:        "stable system prompt",
		Messages:      []domain.LLMMessage{{Role: domain.LLMRoleUser, Text: "<instrument_data>{}</instrument_data>"}},
		JSONSchema:    briefSchema,
		MaxTokens:     4000,
		Effort:        domain.LLMEffortLow,
	}
}

func TestGenerateSendsTheRequestShape(t *testing.T) {
	rp, srv := newReplay(t, http.StatusOK, "brief_ok.json", "application/json")

	if _, err := newTestLLM(srv).Generate(context.Background(), briefReq()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.path != "/v1/messages" {
		t.Fatalf("path = %q", rp.path)
	}
	// The v2 SDK sends the API key as a bearer credential.
	if got := rp.headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q", got)
	}
	if beta := rp.headers.Get("Anthropic-Beta"); !strings.Contains(beta, "server-side-fallback-2026-07-01") {
		t.Fatalf("anthropic-beta = %q, want the server-side fallback beta", beta)
	}
	b := rp.body
	if b["model"] != "claude-opus-5-5" || b["max_tokens"] != float64(4000) {
		t.Fatalf("model/max_tokens = %v/%v", b["model"], b["max_tokens"])
	}
	if b["fallbacks"] != "default" {
		t.Fatalf("fallbacks = %#v, want \"default\"", b["fallbacks"])
	}
	for _, forbidden := range []string{"tool_choice", "thinking", "temperature", "output_format"} {
		if _, ok := b[forbidden]; ok {
			t.Fatalf("request must not send %s: %v", forbidden, b[forbidden])
		}
	}
	oc, _ := b["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Fatalf("output_config.effort = %v", oc["effort"])
	}
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Fatalf("output_config.format = %#v", format)
	}
	system, _ := b["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %#v", b["system"])
	}
	first, _ := system[0].(map[string]any)
	cc, _ := first["cache_control"].(map[string]any)
	if first["text"] != "stable system prompt" || cc["type"] != "ephemeral" {
		t.Fatalf("system[0] = %#v, want the stable prompt with ephemeral cache_control", first)
	}
	msgs, _ := b["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages = %#v", b["messages"])
	}
}

func TestGenerateResponses(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		fixture      string
		wantErr      error
		wantStop     domain.LLMStopReason
		wantUsage    domain.LLMUsage
		wantModel    string
		wantFallback string
		wantContent  string
	}{
		{
			name: "structured brief", status: 200, fixture: "brief_ok.json",
			wantStop: domain.LLMStopEndTurn, wantUsage: domain.LLMUsage{Input: 312, Output: 145, CacheRead: 1024},
			wantModel: "claude-opus-5-5", wantContent: `{"summary":"Fjord Robotics builds port robots`,
		},
		{
			name: "refusal", status: 200, fixture: "refusal.json", wantErr: domain.ErrAIRefused,
			wantStop: domain.LLMStopRefusal, wantUsage: domain.LLMUsage{Input: 290}, wantModel: "claude-opus-5-5",
		},
		{
			name: "max_tokens is an error", status: 200, fixture: "max_tokens.json", wantErr: domain.ErrAIUnavailable,
			wantStop: domain.LLMStopMaxTokens, wantUsage: domain.LLMUsage{Input: 300, Output: 64, CacheWrite: 1100}, wantModel: "claude-opus-5-5",
		},
		{
			name: "server-side fallback answered", status: 200, fixture: "fallback.json",
			wantStop: domain.LLMStopEndTurn, wantUsage: domain.LLMUsage{Input: 310, Output: 40},
			wantModel: "claude-opus-4-8", wantFallback: "claude-opus-4-8", wantContent: `{"summary":"s"`,
		},
		{
			name: "overloaded 529", status: 529, fixture: "overloaded.json", wantErr: domain.ErrAIUnavailable, wantModel: "claude-opus-5-5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, srv := newReplay(t, tc.status, tc.fixture, "application/json")
			resp, err := newTestLLM(srv).Generate(context.Background(), briefReq())
			if tc.wantErr == nil && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if resp.StopReason != tc.wantStop || resp.Usage != tc.wantUsage || resp.Model != tc.wantModel || resp.FallbackModel != tc.wantFallback {
				t.Fatalf("resp = %+v, want stop=%s usage=%+v model=%s fallback=%q", resp, tc.wantStop, tc.wantUsage, tc.wantModel, tc.wantFallback)
			}
			if !strings.HasPrefix(resp.Content, tc.wantContent) {
				t.Fatalf("content = %q, want prefix %q (thinking and fallback blocks are not text)", resp.Content, tc.wantContent)
			}
		})
	}
}

func TestGenerateTimeoutIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newTestLLM(srv).Generate(ctx, briefReq())
	if !errors.Is(err, domain.ErrAIUnavailable) {
		t.Fatalf("err = %v, want ErrAIUnavailable", err)
	}
}

func TestStreamForwardsTextDeltas(t *testing.T) {
	_, srv := newReplay(t, http.StatusOK, "stream_ok.sse", "text/event-stream")
	var deltas []string
	resp, err := newTestLLM(srv).Stream(context.Background(), briefReq(), func(e domain.LLMEvent) error {
		if e.Type == domain.LLMEventText {
			deltas = append(deltas, e.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.Join(deltas, "") != "Hello, beginner." || resp.Content != "Hello, beginner." {
		t.Fatalf("deltas = %q, content = %q", deltas, resp.Content)
	}
	if resp.StopReason != domain.LLMStopEndTurn || resp.Usage.Input != 20 || resp.Usage.Output != 7 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestParamsMapsToolsAndHistory(t *testing.T) {
	l := New("", "claude-opus-5-5", "")
	req := domain.LLMRequest{
		Tools: []domain.LLMTool{{Name: "quote", Description: "Get a quote", InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}},
			"required": []string{"id"}, "additionalProperties": false,
		}}},
		Messages: []domain.LLMMessage{
			{Role: domain.LLMRoleUser, Text: "price?"},
			{Role: domain.LLMRoleAssistant, ToolCalls: []domain.LLMToolCall{{ID: "tu_1", Name: "quote", Input: json.RawMessage(`{"id":"SIM-FJRD"}`)}}},
			{Role: domain.LLMRoleUser, ToolResults: []domain.LLMToolResult{{ToolCallID: "tu_1", Content: "100"}}},
		},
	}
	p, err := l.params(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	s := string(raw)
	for _, want := range []string{`"name":"quote"`, `"additionalProperties":false`, `"tool_use_id":"tu_1"`, `"type":"tool_use"`, `"max_tokens":16000`} {
		if !strings.Contains(s, want) {
			t.Fatalf("params JSON lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "tool_choice") {
		t.Fatal("never send tool_choice")
	}
	if _, err := l.params(domain.LLMRequest{Messages: []domain.LLMMessage{{Role: domain.LLMRoleUser}}}); !errors.Is(err, domain.ErrAIUnavailable) {
		t.Fatalf("empty message: err = %v", err)
	}
}
