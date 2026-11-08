package services

import (
	"fmt"
	"strings"

	"backend/internal/domain"
)

// ParseStreamIDs validates the ids query of GET /market/stream (story NW-09): comma-separated orderbook ids,
// trimmed, blank entries ignored, duplicates kept once (first occurrence order). No entry, or more than
// domain.MaxStreamIDs entries (duplicates count), is a *domain.ValidationError.
func ParseStreamIDs(raw string) ([]string, error) {
	var entries []string
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			entries = append(entries, id)
		}
	}
	if len(entries) == 0 {
		return nil, &domain.ValidationError{Msg: "ids must list at least one orderbook id"}
	}
	if len(entries) > domain.MaxStreamIDs {
		return nil, &domain.ValidationError{Msg: fmt.Sprintf("ids must list at most %d orderbook ids", domain.MaxStreamIDs)}
	}
	seen := make(map[string]bool, len(entries))
	ids := entries[:0:0]
	for _, id := range entries {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}
