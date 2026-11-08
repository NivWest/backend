package app

import (
	"context"
	"log"

	"backend/internal/integrations/database"
	"gorm.io/gorm"
)

type leaderboardRefresher interface {
	RefreshLeaderboard(ctx context.Context) (int, error)
}

// runLeaderboardScheduler periodically recalculates and materializes leaderboard rankings.
// It acquires a Postgres session advisory lock so only one replica per cluster is active leader (story NW-13).
func runLeaderboardScheduler(ctx context.Context, t ticker, s leaderboardRefresher, db *gorm.DB) {
	defer t.Stop()

	var lock *database.AdvisoryLock
	defer func() {
		if lock != nil {
			_ = lock.Release(context.Background())
		}
	}()

	runCycle := func() {
		if ctx.Err() != nil {
			return
		}

		if lock == nil || !lock.IsHeld() || lock.Ping(ctx) != nil {
			if lock != nil {
				_ = lock.Release(context.Background())
				lock = nil
			}
			var acquired bool
			var err error
			lock, acquired, err = database.TryAcquireAdvisoryLock(ctx, db, database.LeaderboardLockKey)
			if err != nil {
				log.Printf("leaderboard advisory lock error: %v", err)
				return
			}
			if !acquired {
				return
			}
		}

		n, err := s.RefreshLeaderboard(ctx)
		if err != nil {
			log.Printf("leaderboard refresh failed: %v", err)
			return
		}
		if n > 0 {
			log.Printf("leaderboard: refreshed %d entries", n)
		}
	}

	// Run once initially
	runCycle()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runCycle()
		}
	}
}
