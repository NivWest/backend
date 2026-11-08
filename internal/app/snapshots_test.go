package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeSnapshotter struct {
	mu    sync.Mutex
	calls int
	err   error
	done  chan struct{}
}

func (f *fakeSnapshotter) SnapshotAll(context.Context) (int, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	f.done <- struct{}{}
	return 3, f.err
}

func (f *fakeSnapshotter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestRunSnapshotScheduler(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{{"snapshots on every tick", nil}, {"keeps running after a failed round", errors.New("db down")}} {
		t.Run(tt.name, func(t *testing.T) {
			ticks := make(chan time.Time)
			stopped := make(chan struct{})
			s := &fakeSnapshotter{err: tt.err, done: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			exited := make(chan struct{})
			go func() {
				runSnapshotScheduler(ctx, ticker{C: ticks, Stop: func() { close(stopped) }}, s)
				close(exited)
			}()

			if s.count() != 0 {
				t.Fatal("nothing may run before the first tick")
			}
			for i := 1; i <= 3; i++ {
				ticks <- time.Now()
				<-s.done
				if got := s.count(); got != i {
					t.Fatalf("after %d ticks: %d rounds", i, got)
				}
			}

			cancel()
			select {
			case <-exited:
			case <-time.After(time.Second):
				t.Fatal("the scheduler did not stop when ctx was cancelled")
			}
			select {
			case <-stopped:
			default:
				t.Error("the ticker was not stopped")
			}
		})
	}
}
