package fake

import (
	"encoding/json"
	"errors"
	"fmt"

	"backend/internal/ai/prompts"
	"backend/internal/domain"
)

// FakeHallucinatedOrderID is the non-existent order ID injected by the fake into the first observation
// so server-side citation validation can be verified deterministically (story NW-17).
const FakeHallucinatedOrderID uint = 999999999

// ReviewDraft is the structured output shape returned by the LLM for insights_review.
type ReviewDraft struct {
	Headline     string                     `json:"headline"`
	Observations []domain.ReviewObservation `json:"observations"`
	HabitToTry   string                     `json:"habit_to_try"`
}

func insightsReview(req domain.LLMRequest) (string, error) {
	raw, ok := prompts.ExtractTagged(lastUserText(req), prompts.InsightsDataTag)
	if !ok {
		return "", errors.New("no <insights_data> block in the prompt")
	}
	var d prompts.InsightsReviewData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return "", fmt.Errorf("decode insights data: %w", err)
	}

	observations := []domain.ReviewObservation{}
	for _, det := range d.Detectors {
		if !det.Triggered {
			continue
		}
		orderIDs := make([]uint, len(det.Evidence.OrderIDs))
		copy(orderIDs, det.Evidence.OrderIDs)
		if len(observations) == 0 {
			orderIDs = append(orderIDs, FakeHallucinatedOrderID)
		}
		observations = append(observations, domain.ReviewObservation{
			DetectorID: string(det.ID),
			Narrative:  "FAKE: " + det.Title,
			OrderIDs:   orderIDs,
		})
	}

	draft := ReviewDraft{
		Headline:     "FAKE: weekly review",
		Observations: observations,
		HabitToTry:   "FAKE: Write a short trade thesis before every order and keep each position under 40% of equity.",
	}
	out, err := json.Marshal(draft)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
