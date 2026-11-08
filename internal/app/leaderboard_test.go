package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

type fakeLeaderboardRefresher struct {
	mu    sync.Mutex
	calls int
	err   error
	done  chan struct{}
}

func (f *fakeLeaderboardRefresher) RefreshLeaderboard(context.Context) (int, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.done <- struct{}{}
	return 2, f.err
}

func (f *fakeLeaderboardRefresher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestRunLeaderboardScheduler(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"refreshes on start and every tick", nil},
		{"keeps running after failed cycle", errors.New("db error")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ticks := make(chan time.Time)
			stopped := make(chan struct{})
			s := &fakeLeaderboardRefresher{err: tt.err, done: make(chan struct{}, 10)}
			ctx, cancel := context.WithCancel(context.Background())
			exited := make(chan struct{})

			// Fake non-postgres DB so advisory lock is no-op
			db := &gorm.DB{Config: &gorm.Config{}}

			go func() {
				runLeaderboardScheduler(ctx, ticker{C: ticks, Stop: func() { close(stopped) }}, s, db)
				close(exited)
			}()

			// Should run once initially
			select {
			case <-s.done:
			case <-time.After(time.Second):
				t.Fatal("initial run did not trigger")
			}
			if s.count() != 1 {
				t.Fatalf("expected 1 initial run, got %d", s.count())
			}

			// Tick 2 more times
			for i := 2; i <= 3; i++ {
				ticks <- time.Now()
				select {
				case <-s.done:
				case <-time.After(time.Second):
					t.Fatalf("tick %d did not trigger", i)
				}
				if got := s.count(); got != i {
					t.Fatalf("after %d calls: got %d", i, got)
				}
			}

			cancel()
			select {
			case <-exited:
			case <-time.After(time.Second):
				t.Fatal("scheduler did not exit on context cancel")
			}
			select {
			case <-stopped:
			default:
				t.Error("ticker was not stopped")
			}
		})
	}
}
