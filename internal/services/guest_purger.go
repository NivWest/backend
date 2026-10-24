package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"backend/internal/domain"
)

// GuestPurger periodically hard-deletes guest accounts whose sessions expired more than
// retention ago (story NW-01).
type GuestPurger struct {
	repo      domain.GuestRepository
	retention time.Duration
	now       func() time.Time
}

// NewGuestPurger returns a purger that deletes guests idle for longer than retention.
// A nil now defaults to time.Now.
func NewGuestPurger(repo domain.GuestRepository, retention time.Duration, now func() time.Time) *GuestPurger {
	if now == nil {
		now = time.Now
	}
	return &GuestPurger{repo: repo, retention: retention, now: now}
}

// PurgeOnce deletes every expired guest older than now - retention and returns how many were deleted.
func (p *GuestPurger) PurgeOnce(ctx context.Context) (int64, error) {
	deleted, err := p.repo.DeleteExpiredGuests(ctx, p.now().Add(-p.retention))
	if err != nil {
		return 0, fmt.Errorf("purge expired guests: %w", err)
	}
	return deleted, nil
}

// Run purges once immediately and then on every tick of interval until ctx is done.
// It blocks, so start it in its own goroutine. Only counts and errors are logged.
func (p *GuestPurger) Run(ctx context.Context, interval time.Duration) {
	p.purgeAndLog(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.purgeAndLog(ctx)
		}
	}
}

func (p *GuestPurger) purgeAndLog(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	deleted, err := p.PurgeOnce(ctx)
	if err != nil {
		log.Printf("guest purge failed: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("guest purge: deleted %d expired guest accounts", deleted)
	}
}
