package domain

import (
	"context"
	"encoding/json"
	"time"
)

// LLM port (story NW-14). Every model call goes through services.AIGateway, which wraps one LLM
// implementation: internal/integrations/anthropic (Claude) or internal/llm/fake (deterministic, for tests).
// The types are provider-neutral: no SDK type leaves an adapter. Tools and Stream are used by the chat
// (NW-16); the instrument brief only needs Generate with a JSON schema.

// LLM provider names, selected by LLM_PROVIDER and reported by GET /health as llm_provider.
const (
	LLMProviderAnthropic = "anthropic"
	LLMProviderFake      = "fake"
)

// LLMRole is the author of one message.
type LLMRole string

const (
	LLMRoleUser      LLMRole = "user"
	LLMRoleAssistant LLMRole = "assistant"
)

// LLMEffort controls how much the model thinks (and spends). Empty leaves the provider default.
type LLMEffort string

const (
	LLMEffortLow    LLMEffort = "low"
	LLMEffortMedium LLMEffort = "medium"
	LLMEffortHigh   LLMEffort = "high"
	LLMEffortXHigh  LLMEffort = "xhigh"
	LLMEffortMax    LLMEffort = "max"
)

// LLMTool is a client-side tool the model may call. InputSchema is a JSON schema object.
type LLMTool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// LLMToolCall is one tool invocation requested by the model. Input is the raw JSON arguments:
// parse it with encoding/json and validate it before running the tool.
type LLMToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// LLMToolResult answers one LLMToolCall in the next user message.
type LLMToolResult struct {
	ToolCallID string
	Content    string
	IsError    bool
}

// LLMMessage is one conversation turn. A user turn carries Text and/or ToolResults; an assistant turn
// carries Text and/or ToolCalls.
type LLMMessage struct {
	Role        LLMRole
	Text        string
	ToolCalls   []LLMToolCall
	ToolResults []LLMToolResult
}

// LLMDocument is a document content block sent to the model for native citations (story NW-15).
type LLMDocument struct {
	Title     string
	Content   string
	Citations bool
}

// LLMRequest is one model call.
type LLMRequest struct {
	// Feature and PromptVersion identify the caller in the usage ledger (e.g. instrument_brief, instrument_brief.v1).
	Feature       AIFeature
	PromptVersion string
	// System is the stable instructions; adapters put it first and mark it cacheable. Never put
	// per-request data in it, or prompt caching stops working.
	System    string
	Messages  []LLMMessage
	Tools     []LLMTool
	Documents []LLMDocument
	// JSONSchema, when set, constrains the answer to JSON matching it (structured output). Content is then
	// the JSON document.
	JSONSchema map[string]any
	MaxTokens  int64
	Effort     LLMEffort
}

// LLMStopReason is why the model stopped.
type LLMStopReason string

const (
	LLMStopEndTurn   LLMStopReason = "end_turn"
	LLMStopToolUse   LLMStopReason = "tool_use"
	LLMStopMaxTokens LLMStopReason = "max_tokens"
	LLMStopRefusal   LLMStopReason = "refusal"
	LLMStopOther     LLMStopReason = "other"
)

// LLMUsage is the token count of one call. Input excludes cached tokens (CacheRead) and tokens written to
// the cache (CacheWrite).
type LLMUsage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

// Total is every token the call processed; the daily budget counts this.
func (u LLMUsage) Total() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// LLMCitation represents a native citation returned by the model from a document block (story NW-15).
type LLMCitation struct {
	DocumentIndex  int
	DocumentTitle  string
	CitedText      string
	StartCharIndex int
	EndCharIndex   int
}

// LLMResponse is the result of one call.
type LLMResponse struct {
	// Content is the concatenated text of the answer (the JSON document with LLMRequest.JSONSchema).
	Content    string
	ToolCalls  []LLMToolCall
	Citations  []LLMCitation
	StopReason LLMStopReason
	Usage      LLMUsage
	// Model is the model that produced the answer: the fallback model when FallbackModel is set.
	Model string
	// FallbackModel is set when the requested model declined and a server-side fallback model answered.
	FallbackModel string
	Latency       time.Duration
}

// LLMEventType tags a streamed event.
type LLMEventType string

const (
	// LLMEventText carries a piece of answer text in Text.
	LLMEventText LLMEventType = "text"
	// LLMEventToolCall carries a complete tool call in ToolCall.
	LLMEventToolCall LLMEventType = "tool_call"
)

// LLMEvent is one streamed event.
type LLMEvent struct {
	Type     LLMEventType
	Text     string
	ToolCall *LLMToolCall
}

// LLM is the model port. Implementations map provider failures to the AI errors below:
//   - a refusal returns the response (with its usage) and an error wrapping ErrAIRefused;
//   - an answer cut off at MaxTokens returns the response and an error wrapping ErrAIUnavailable;
//   - network errors, timeouts, rate limits and 5xx wrap ErrAIUnavailable.
type LLM interface {
	// Name is the provider name: LLMProviderAnthropic or LLMProviderFake.
	Name() string
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
	// Stream calls onEvent for every text delta and completed tool call, in order, and returns the full
	// response at the end. An error from onEvent aborts the stream and is returned.
	Stream(ctx context.Context, req LLMRequest, onEvent func(LLMEvent) error) (LLMResponse, error)
}
