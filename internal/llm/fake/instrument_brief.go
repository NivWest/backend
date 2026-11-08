package fake

import (
	"encoding/json"
	"errors"
	"fmt"

	"backend/internal/ai/prompts"
	"backend/internal/domain"
)

// instrumentBrief answers instrument_brief from the JSON inside <instrument_data>:
//
//	summary = "FAKE: <name> (<symbol>) trades at <last, 2 decimals> SEK."
//
// Every other field is a short string derived from the input, prefixed "FAKE:".
func instrumentBrief(req domain.LLMRequest) (string, error) {
	raw, ok := prompts.ExtractTagged(lastUserText(req), prompts.InstrumentDataTag)
	if !ok {
		return "", errors.New("no <instrument_data> block in the prompt")
	}
	var d prompts.InstrumentData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return "", fmt.Errorf("decode instrument data: %w", err)
	}

	metrics := []domain.BriefMetric{
		{Metric: "Last price", Value: fmt.Sprintf("%.2f SEK", d.LastPrice), Explanation: fmt.Sprintf("FAKE: the price of one %s share.", d.Symbol)},
		{Metric: "Day change", Value: fmt.Sprintf("%+.2f%%", d.ChangePercent), Explanation: "FAKE: how much the price moved today."},
	}
	if d.PERatio != nil {
		metrics = append(metrics, domain.BriefMetric{Metric: "P/E ratio", Value: fmt.Sprintf("%.2f", *d.PERatio), Explanation: "FAKE: the price divided by one year of earnings per share."})
	}
	if d.DividendYieldPct != nil {
		metrics = append(metrics, domain.BriefMetric{Metric: "Dividend yield", Value: fmt.Sprintf("%.2f%%", *d.DividendYieldPct), Explanation: "FAKE: yearly dividends as a share of the price."})
	}
	if d.MarketCap != nil {
		metrics = append(metrics, domain.BriefMetric{Metric: "Market cap", Value: fmt.Sprintf("%.0f %s", *d.MarketCap, d.Currency), Explanation: "FAKE: what all shares are worth together."})
	}
	sector := d.Sector
	if sector == "" {
		sector = "Unknown"
	}
	brief := domain.Brief{
		Summary:           fmt.Sprintf("FAKE: %s (%s) trades at %.2f SEK.", d.Name, d.Symbol, d.LastPrice),
		WhatTheNumbersSay: metrics,
		Risks: []string{
			fmt.Sprintf("FAKE: %s companies move with their sector.", sector),
			fmt.Sprintf("FAKE: the price of %s can fall as well as rise.", d.Symbol),
		},
		BeginnerTakeaway: fmt.Sprintf("FAKE: learn what %s does before you trade %s.", d.Name, d.Symbol),
	}
	out, err := json.Marshal(brief)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
