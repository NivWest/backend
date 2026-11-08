// Package fake is the deterministic LLM provider (LLM_PROVIDER=fake, story NW-14). It never touches the
// network: every feature has a script in this package that derives the answer from the request, so E2E
// tests can assert exact text. Dev/test only.
//
// Usage is input_tokens = ceil(len(prompt)/4) and output_tokens = ceil(len(output)/4), where prompt is
// the system prompt and every message text joined with "\n" and len counts bytes. Latency is 0.
package fake

import (
	"context"
	"fmt"
	"strings"

	"backend/internal/domain"
)

// Model is the model name the fake reports.
const Model = "fake"

// script answers one feature's request.
type script func(req domain.LLMRequest) (string, error)

var scripts = map[domain.AIFeature]script{
	domain.AIFeatureInstrumentBrief: instrumentBrief,
}

// LLM is the fake domain.LLM.
type LLM struct{}

var _ domain.LLM = LLM{}

func New() LLM { return LLM{} }

func (LLM) Name() string { return domain.LLMProviderFake }

func (LLM) Generate(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	if err := ctx.Err(); err != nil {
		return domain.LLMResponse{}, fmt.Errorf("%w: %w", domain.ErrAIUnavailable, err)
	}
	run, ok := scripts[req.Feature]
	if !ok {
		return domain.LLMResponse{}, fmt.Errorf("%w: the fake provider has no script for feature %q", domain.ErrAIUnavailable, req.Feature)
	}
	out, err := run(req)
	if err != nil {
		return domain.LLMResponse{}, fmt.Errorf("%w: fake %s: %w", domain.ErrAIUnavailable, req.Feature, err)
	}
	return domain.LLMResponse{
		Content:    out,
		StopReason: domain.LLMStopEndTurn,
		Usage:      domain.LLMUsage{Input: ceilQuarter(len(Prompt(req))), Output: ceilQuarter(len(out))},
		Model:      Model,
	}, nil
}

// Stream emits the whole answer as one text event.
func (f LLM) Stream(ctx context.Context, req domain.LLMRequest, onEvent func(domain.LLMEvent) error) (domain.LLMResponse, error) {
	resp, err := f.Generate(ctx, req)
	if err != nil {
		return resp, err
	}
	if err := onEvent(domain.LLMEvent{Type: domain.LLMEventText, Text: resp.Content}); err != nil {
		return resp, err
	}
	return resp, nil
}

// Prompt is the text the fake counts input tokens on: the system prompt and every message text, joined with "\n".
func Prompt(req domain.LLMRequest) string {
	parts := make([]string, 0, len(req.Messages)+1)
	parts = append(parts, req.System)
	for _, m := range req.Messages {
		parts = append(parts, m.Text)
	}
	return strings.Join(parts, "\n")
}

func ceilQuarter(n int) int64 { return int64((n + 3) / 4) }

// lastUserText is the text of the last user message.
func lastUserText(req domain.LLMRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == domain.LLMRoleUser {
			return req.Messages[i].Text
		}
	}
	return ""
}
