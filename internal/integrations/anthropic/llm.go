// Package anthropic is the Claude adapter of the domain.LLM port (story NW-14), built on the official SDK
// github.com/anthropics/anthropic-sdk-go (v2). It speaks only domain types to the rest of the backend.
//
// Every request:
//   - uses the configured model (LLM_MODEL, default claude-opus-5-5) with adaptive thinking (the model's
//     default; it cannot be turned off, so depth is controlled with output_config.effort per feature);
//   - puts the stable system prompt first with cache_control, so repeated calls read it from the prompt cache;
//   - asks for structured output through output_config.format (a JSON schema), never a forced tool_choice
//     (which this model rejects with a 400);
//   - opts into the server-side refusal fallback (fallbacks: "default", beta server-side-fallback-2026-07-01)
//     and reports in LLMResponse.FallbackModel when a fallback model answered.
//
// The response's stop_reason is always checked: refusal → domain.ErrAIRefused, max_tokens →
// domain.ErrAIUnavailable (a cut-off answer is never returned as valid). Transport and API errors wrap
// domain.ErrAIUnavailable. Unit tests replay recorded JSON from an httptest server (option.WithBaseURL).
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"backend/internal/domain"

	sdk "github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"
	"github.com/anthropics/anthropic-sdk-go/v2/shared/constant"
)

// DefaultMaxTokens is used when a request leaves MaxTokens at 0.
const DefaultMaxTokens int64 = 16000

// LLM is the Claude implementation of domain.LLM.
type LLM struct {
	client sdk.Client
	model  string
	now    func() time.Time
}

var _ domain.LLM = (*LLM)(nil)

// New builds the adapter. apiKey is ANTHROPIC_API_KEY (an empty key lets the SDK fall back to its own
// credential lookup); baseURL overrides the API URL when non-empty. Extra options are appended last (tests
// pass option.WithMaxRetries(0)).
func New(apiKey, model, baseURL string, opts ...option.RequestOption) *LLM {
	var all []option.RequestOption
	if apiKey != "" {
		all = append(all, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		all = append(all, option.WithBaseURL(baseURL))
	}
	all = append(all, opts...)
	return &LLM{client: sdk.NewClient(all...), model: model, now: time.Now}
}

func (l *LLM) Name() string { return domain.LLMProviderAnthropic }

// Generate sends one request and waits for the whole message.
func (l *LLM) Generate(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	params, err := l.params(req)
	if err != nil {
		return domain.LLMResponse{}, err
	}
	start := l.now()
	msg, err := l.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return domain.LLMResponse{Model: l.model, Latency: l.now().Sub(start)}, mapError(err)
	}
	resp := toResponse(msg)
	resp.Latency = l.now().Sub(start)
	return resp, checkStop(resp)
}

// Stream sends one streaming request, forwarding text deltas and completed tool calls to onEvent.
func (l *LLM) Stream(ctx context.Context, req domain.LLMRequest, onEvent func(domain.LLMEvent) error) (domain.LLMResponse, error) {
	params, err := l.params(req)
	if err != nil {
		return domain.LLMResponse{}, err
	}
	start := l.now()
	stream := l.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var acc sdk.BetaMessage
	for stream.Next() {
		ev := stream.Current()
		if err := acc.Accumulate(ev); err != nil {
			return domain.LLMResponse{Model: l.model, Latency: l.now().Sub(start)}, fmt.Errorf("%w: accumulate stream: %w", domain.ErrAIUnavailable, err)
		}
		var cbErr error
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				cbErr = onEvent(domain.LLMEvent{Type: domain.LLMEventText, Text: ev.Delta.Text})
			}
		case "content_block_stop":
			if i := int(ev.Index); i >= 0 && i < len(acc.Content) && acc.Content[i].Type == "tool_use" {
				b := acc.Content[i]
				cbErr = onEvent(domain.LLMEvent{Type: domain.LLMEventToolCall, ToolCall: &domain.LLMToolCall{ID: b.ID, Name: b.Name, Input: b.Input}})
			}
		}
		if cbErr != nil {
			return toResponseLatency(&acc, l.now().Sub(start)), cbErr
		}
	}
	if err := stream.Err(); err != nil {
		resp := toResponseLatency(&acc, l.now().Sub(start))
		if resp.Model == "" {
			resp.Model = l.model
		}
		return resp, mapError(err)
	}
	resp := toResponseLatency(&acc, l.now().Sub(start))
	return resp, checkStop(resp)
}

func (l *LLM) params(req domain.LLMRequest) (sdk.BetaMessageNewParams, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	p := sdk.BetaMessageNewParams{
		Model:     sdk.Model(l.model),
		MaxTokens: maxTokens,
		Messages:  make([]sdk.BetaMessageParam, 0, len(req.Messages)),
		Fallbacks: sdk.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()},
		Betas:     []sdk.AnthropicBeta{sdk.AnthropicBetaServerSideFallback2026_07_01},
	}
	if req.System != "" {
		p.System = []sdk.BetaTextBlockParam{{Text: req.System, CacheControl: sdk.NewBetaCacheControlEphemeralParam()}}
	}
	if req.Effort != "" {
		p.OutputConfig.Effort = sdk.BetaOutputConfigEffort(req.Effort)
	}
	if req.JSONSchema != nil {
		p.OutputConfig.Format = sdk.BetaJSONOutputFormatParam{Schema: req.JSONSchema}
	}
	for _, t := range req.Tools {
		schema := sdk.BetaToolInputSchemaParam{ExtraFields: map[string]any{}}
		for k, v := range t.InputSchema {
			switch k {
			case "type":
			case "properties":
				schema.Properties = v
			case "required":
				if required, ok := v.([]string); ok {
					schema.Required = required
				}
			default:
				schema.ExtraFields[k] = v
			}
		}
		tool := sdk.BetaToolParam{Name: t.Name, InputSchema: schema}
		if t.Description != "" {
			tool.Description = param.NewOpt(t.Description)
		}
		p.Tools = append(p.Tools, sdk.BetaToolUnionParam{OfTool: &tool})
	}
	for _, m := range req.Messages {
		var blocks []sdk.BetaContentBlockParamUnion
		if m.Text != "" {
			blocks = append(blocks, sdk.NewBetaTextBlock(m.Text))
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, sdk.NewBetaToolUseBlock(tc.ID, tc.Input, tc.Name))
		}
		for _, tr := range m.ToolResults {
			blocks = append(blocks, sdk.NewBetaToolResultBlock(tr.ToolCallID, tr.Content, tr.IsError))
		}
		if len(blocks) == 0 {
			return p, fmt.Errorf("%w: empty %s message", domain.ErrAIUnavailable, m.Role)
		}
		switch m.Role {
		case domain.LLMRoleUser:
			p.Messages = append(p.Messages, sdk.BetaMessageParam{OfUser: &sdk.BetaUserMessageParam{Content: blocks}})
		case domain.LLMRoleAssistant:
			p.Messages = append(p.Messages, sdk.BetaMessageParam{OfAssistant: &sdk.BetaAssistantMessageParam{Content: blocks}})
		default:
			return p, fmt.Errorf("%w: unknown message role %q", domain.ErrAIUnavailable, m.Role)
		}
	}
	return p, nil
}

func toResponseLatency(msg *sdk.BetaMessage, latency time.Duration) domain.LLMResponse {
	resp := toResponse(msg)
	resp.Latency = latency
	return resp
}

func toResponse(msg *sdk.BetaMessage) domain.LLMResponse {
	var text strings.Builder
	var calls []domain.LLMToolCall
	for _, b := range msg.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			calls = append(calls, domain.LLMToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	resp := domain.LLMResponse{
		Content:   text.String(),
		ToolCalls: calls,
		Model:     string(msg.Model),
		Usage: domain.LLMUsage{
			Input:      msg.Usage.InputTokens,
			Output:     msg.Usage.OutputTokens,
			CacheRead:  msg.Usage.CacheReadInputTokens,
			CacheWrite: msg.Usage.CacheCreationInputTokens,
		},
	}
	// A fallback_message iteration means a fallback model ran; msg.Model names the model that answered.
	for _, it := range msg.Usage.Iterations {
		if it.Type == "fallback_message" {
			resp.FallbackModel = string(msg.Model)
		}
	}
	switch msg.StopReason {
	case sdk.BetaStopReasonEndTurn, sdk.BetaStopReasonStopSequence:
		resp.StopReason = domain.LLMStopEndTurn
	case sdk.BetaStopReasonToolUse:
		resp.StopReason = domain.LLMStopToolUse
	case sdk.BetaStopReasonMaxTokens:
		resp.StopReason = domain.LLMStopMaxTokens
	case sdk.BetaStopReasonRefusal:
		resp.StopReason = domain.LLMStopRefusal
	default:
		resp.StopReason = domain.LLMStopOther
	}
	return resp
}

// checkStop turns a stop reason that is not a usable answer into an error.
func checkStop(resp domain.LLMResponse) error {
	switch resp.StopReason {
	case domain.LLMStopEndTurn, domain.LLMStopToolUse:
		return nil
	case domain.LLMStopRefusal:
		return domain.ErrAIRefused
	case domain.LLMStopMaxTokens:
		return fmt.Errorf("%w: the answer was cut off at max_tokens", domain.ErrAIUnavailable)
	default:
		return fmt.Errorf("%w: unexpected stop reason", domain.ErrAIUnavailable)
	}
}

// mapError wraps any SDK or transport error in domain.ErrAIUnavailable, keeping the HTTP status in the text.
// The API key is never part of the SDK's error text.
func mapError(err error) error {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%w: anthropic API status %d: %w", domain.ErrAIUnavailable, apiErr.StatusCode, err)
	}
	return fmt.Errorf("%w: %w", domain.ErrAIUnavailable, err)
}
