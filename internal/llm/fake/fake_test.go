package fake

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"backend/internal/ai/prompts"
	"backend/internal/domain"
)

func briefRequest(t *testing.T, d prompts.InstrumentData) domain.LLMRequest {
	t.Helper()
	p, err := prompts.InstrumentBrief(d)
	if err != nil {
		t.Fatal(err)
	}
	return domain.LLMRequest{
		Feature: domain.AIFeatureInstrumentBrief, PromptVersion: p.Version, System: p.System,
		Messages: []domain.LLMMessage{{Role: domain.LLMRoleUser, Text: p.User}},
	}
}

func TestInstrumentBriefScript(t *testing.T) {
	pe, cap := 14.25, 1.8e10
	cases := []struct {
		name        string
		data        prompts.InstrumentData
		wantSummary string
		wantMetrics int
	}{
		{"pinned at 100", prompts.InstrumentData{Name: "Fjord Robotics", Symbol: "FJRD", Sector: "Industrials", LastPrice: 100, PERatio: &pe, MarketCap: &cap, Currency: "SEK"},
			"FAKE: Fjord Robotics (FJRD) trades at 100.00 SEK.", 4},
		{"two decimals", prompts.InstrumentData{Name: "Nordvik Energy", Symbol: "NVE", LastPrice: 132.5},
			"FAKE: Nordvik Energy (NVE) trades at 132.50 SEK.", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := briefRequest(t, tc.data)
			resp, err := New().Generate(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			var b domain.Brief
			if err := json.Unmarshal([]byte(resp.Content), &b); err != nil {
				t.Fatalf("content is not a Brief: %v", err)
			}
			if b.Summary != tc.wantSummary {
				t.Fatalf("summary = %q, want %q", b.Summary, tc.wantSummary)
			}
			if len(b.WhatTheNumbersSay) != tc.wantMetrics || len(b.Risks) == 0 || b.BeginnerTakeaway == "" {
				t.Fatalf("brief = %+v", b)
			}
			wantIn := int64((len(Prompt(req)) + 3) / 4)
			wantOut := int64((len(resp.Content) + 3) / 4)
			if resp.Usage != (domain.LLMUsage{Input: wantIn, Output: wantOut}) || resp.Model != "fake" || resp.Latency != 0 || resp.StopReason != domain.LLMStopEndTurn {
				t.Fatalf("response = %+v, want usage in=%d out=%d", resp, wantIn, wantOut)
			}
			again, _ := New().Generate(context.Background(), req)
			if again.Content != resp.Content {
				t.Fatal("the fake must be deterministic")
			}
		})
	}
}

func TestCeilQuarter(t *testing.T) {
	for n, want := range map[int]int64{0: 0, 1: 1, 4: 1, 5: 2, 8: 2, 9: 3} {
		if got := ceilQuarter(n); got != want {
			t.Errorf("ceilQuarter(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestFakeErrors(t *testing.T) {
	cases := []struct {
		name string
		req  domain.LLMRequest
	}{
		{"unknown feature", domain.LLMRequest{Feature: "chat"}},
		{"no data block", domain.LLMRequest{Feature: domain.AIFeatureInstrumentBrief, Messages: []domain.LLMMessage{{Role: domain.LLMRoleUser, Text: "hi"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New().Generate(context.Background(), tc.req); !errors.Is(err, domain.ErrAIUnavailable) {
				t.Fatalf("err = %v, want ErrAIUnavailable", err)
			}
		})
	}
}

func TestStreamEmitsContent(t *testing.T) {
	req := briefRequest(t, prompts.InstrumentData{Name: "A", Symbol: "A", LastPrice: 1})
	var got string
	resp, err := New().Stream(context.Background(), req, func(e domain.LLMEvent) error {
		got += e.Text
		return nil
	})
	if err != nil || got != resp.Content || got == "" {
		t.Fatalf("stream = %q, %v; want %q", got, err, resp.Content)
	}
	stop := errors.New("stop")
	if _, err := New().Stream(context.Background(), req, func(domain.LLMEvent) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("callback error = %v, want it returned", err)
	}
}
