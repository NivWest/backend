package app

import (
	"context"
	"log/slog"
	"time"
)

// snapshotter takes one round of equity snapshots (services.SnapshotService.SnapshotAll).
type snapshotter interface {
	SnapshotAll(ctx context.Context) (int, error)
}

// ticker is the clock of the snapshot scheduler: C fires every interval, Stop releases it. Production uses
// time.NewTicker (newTimeTicker); tests inject a channel they control.
type ticker struct {
	C    <-chan time.Time
	Stop func()
}

func newTimeTicker(interval time.Duration) ticker {
	t := time.NewTicker(interval)
	return ticker{C: t.C, Stop: t.Stop}
}

// runSnapshotScheduler snapshots every user with an order on every tick until ctx is done (story NW-10). It
// blocks, so start it in its own goroutine. Nothing runs at start-up, so a restart adds no extra snapshot;
// fills are snapshotted by the order service itself. Only counts and errors are logged.
func runSnapshotScheduler(ctx context.Context, t ticker, s snapshotter) {
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ctx.Err() != nil {
				return
			}
			n, err := s.SnapshotAll(ctx)
			if err != nil {
				slog.Error("equity snapshot round failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("equity snapshots taken", "count", n)
			}
		}
	}
}
