package domain

import "time"

// OrderMetricsObserver receives callbacks when orders are filled.
type OrderMetricsObserver interface {
	ObserveOrderFilled(side, orderType string)
}

// AIMetricsObserver receives callbacks when LLM operations complete.
type AIMetricsObserver interface {
	ObserveLLMCall(feature, provider, status string, latency time.Duration, inputTokens, outputTokens int64, costUSD float64)
}

// QuoteHubMetricsObserver receives callbacks when the live quote subscriber count changes.
type QuoteHubMetricsObserver interface {
	SetSubscribers(count int)
}
