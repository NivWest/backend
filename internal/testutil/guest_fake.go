package testutil

import (
	"context"
	"sync"
	"time"

	"backend/internal/domain"
)

// Compile-time check that the fake satisfies the domain contract.
var _ domain.GuestRepository = (*GuestRepo)(nil)

// ---------------------------------------------------------------------------
// GuestRepository
// ---------------------------------------------------------------------------

// GuestRepo is a fake domain.GuestRepository that records the cutoff of every call.
// By default DeleteExpiredGuests returns (0, nil).
type GuestRepo struct {
	mu    sync.Mutex
	Calls []time.Time

	DeleteExpiredGuestsFn func(ctx context.Context, cutoff time.Time) (int64, error)
}

func (r *GuestRepo) DeleteExpiredGuests(ctx context.Context, cutoff time.Time) (int64, error) {
	r.mu.Lock()
	r.Calls = append(r.Calls, cutoff)
	r.mu.Unlock()
	if r.DeleteExpiredGuestsFn != nil {
		return r.DeleteExpiredGuestsFn(ctx, cutoff)
	}
	return 0, nil
}

// Cutoffs returns a copy of the recorded cutoffs; safe to call while other goroutines
// (e.g. a purge ticker) are still calling DeleteExpiredGuests.
func (r *GuestRepo) Cutoffs() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.Calls...)
}
