package insights

import "backend/internal/domain"

// DefaultDetectors returns the complete suite of deterministic behavioral detectors in canonical order.
func DefaultDetectors() []domain.Detector {
	return []domain.Detector{
		NewConcentrationDetector(),
		NewRevengeTradingDetector(),
		NewOvertradingDetector(),
		NewDispositionEffectDetector(),
		NewNoThesisDetector(),
	}
}
