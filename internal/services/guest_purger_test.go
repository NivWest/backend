package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/testutil"
)

var purgeTestNow = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return purgeTestNow }

func TestGuestPurger_PurgeOnce_UsesNowMinusRetention(t *testing.T) {
	repo := &testutil.GuestRepo{
		DeleteExpiredGuestsFn: func(context.Context, time.Time) (int64, error) { return 3, nil },
	}
	purger := NewGuestPurger(repo, 24*time.Hour, fixedClock)

	deleted, err := purger.PurgeOnce(context.Background())
	if err != nil {
		t.Fatalf("PurgeOnce() error = %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}
	cutoffs := repo.Cutoffs()
	if len(cutoffs) != 1 {
		t.Fatalf("calls = %d, want 1", len(cutoffs))
	}
	if want := purgeTestNow.Add(-24 * time.Hour); !cutoffs[0].Equal(want) {
		t.Errorf("cutoff = %v, want %v", cutoffs[0], want)
	}
}

func TestGuestPurger_PurgeOnce_PropagatesError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &testutil.GuestRepo{
		DeleteExpiredGuestsFn: func(context.Context, time.Time) (int64, error) { return 0, repoErr },
	}
	purger := NewGuestPurger(repo, time.Hour, fixedClock)

	if _, err := purger.PurgeOnce(context.Background()); !errors.Is(err, repoErr) {
		t.Fatalf("error = %v, want %v", err, repoErr)
	}
}

func TestGuestPurger_NilClockDefaultsToTimeNow(t *testing.T) {
	repo := &testutil.GuestRepo{}
	purger := NewGuestPurger(repo, time.Hour, nil)

	before := time.Now()
	if _, err := purger.PurgeOnce(context.Background()); err != nil {
		t.Fatalf("PurgeOnce() error = %v", err)
	}
	cutoff := repo.Cutoffs()[0]
	if cutoff.Before(before.Add(-time.Hour)) || cutoff.After(time.Now().Add(-time.Hour)) {
		t.Errorf("cutoff = %v, want about time.Now() - 1h", cutoff)
	}
}

// waitFor polls cond every few milliseconds for at most timeout.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func TestGuestPurger_Run_PurgesImmediatelyThenOnTicks(t *testing.T) {
	repo := &testutil.GuestRepo{}
	purger := NewGuestPurger(repo, time.Hour, fixedClock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		purger.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	// The interval is 5ms but the first purge must not wait for a tick.
	if !waitFor(t, 2*time.Second, func() bool { return len(repo.Cutoffs()) >= 1 }) {
		t.Fatal("Run did not purge immediately")
	}
	if !waitFor(t, 2*time.Second, func() bool { return len(repo.Cutoffs()) >= 3 }) {
		t.Fatalf("Run purged %d times, want at least 3 with a 5ms interval", len(repo.Cutoffs()))
	}
	for i, c := range repo.Cutoffs() {
		if want := purgeTestNow.Add(-time.Hour); !c.Equal(want) {
			t.Errorf("cutoff[%d] = %v, want %v", i, c, want)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}
	stopped := len(repo.Cutoffs())
	time.Sleep(20 * time.Millisecond)
	if after := len(repo.Cutoffs()); after != stopped {
		t.Errorf("purges after Run returned: %d -> %d", stopped, after)
	}
}

func TestGuestPurger_Run_FirstPurgeIsNotDelayedByInterval(t *testing.T) {
	repo := &testutil.GuestRepo{}
	purger := NewGuestPurger(repo, time.Hour, fixedClock)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		purger.Run(ctx, time.Hour)
		close(done)
	}()

	if !waitFor(t, 2*time.Second, func() bool { return len(repo.Cutoffs()) == 1 }) {
		t.Fatal("Run did not purge immediately with a 1h interval")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}
	if n := len(repo.Cutoffs()); n != 1 {
		t.Errorf("purges = %d, want 1", n)
	}
}

func TestGuestPurger_Run_KeepsGoingAfterErrors(t *testing.T) {
	repo := &testutil.GuestRepo{
		DeleteExpiredGuestsFn: func(context.Context, time.Time) (int64, error) { return 0, errors.New("db down") },
	}
	purger := NewGuestPurger(repo, time.Hour, fixedClock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		purger.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	if !waitFor(t, 2*time.Second, func() bool { return len(repo.Cutoffs()) >= 2 }) {
		t.Fatal("Run stopped purging after an error")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx was cancelled")
	}
}
