package database

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

// LeaderboardLockKey is the 64-bit advisory lock key for leaderboard background refresh (story NW-13).
// ASCII 'NW13' = 0x4e573133 = 1314337075.
const LeaderboardLockKey int64 = 0x4e573133

// AdvisoryLock manages a PostgreSQL session advisory lock (story NW-13).
// A dedicated connection ensures the lock is held across transactions until released
// or until the connection closes.
type AdvisoryLock struct {
	conn *sql.Conn
	key  int64
	mu   sync.Mutex
	held bool
}

// TryAcquireAdvisoryLock attempts to acquire a session advisory lock with the given 64-bit key.
// If the lock is acquired, it returns the AdvisoryLock and true.
// If already held by another session, it returns nil and false.
// If not running on Postgres, it returns an AdvisoryLock and true (no-op for non-postgres databases).
func TryAcquireAdvisoryLock(ctx context.Context, db *gorm.DB, key int64) (*AdvisoryLock, bool, error) {
	if db.Dialector.Name() != "postgres" {
		return &AdvisoryLock{key: key, held: true}, true, nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, false, fmt.Errorf("get sql.DB: %w", err)
	}

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire sql.Conn: %w", err)
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}

	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}

	return &AdvisoryLock{
		conn: conn,
		key:  key,
		held: true,
	}, true, nil
}

// IsHeld reports whether the lock is currently marked as held.
func (l *AdvisoryLock) IsHeld() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// Ping checks whether the underlying connection is still responsive.
func (l *AdvisoryLock) Ping(ctx context.Context) error {
	if l == nil {
		return fmt.Errorf("lock is nil")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held || l.conn == nil {
		return nil
	}
	return l.conn.PingContext(ctx)
}

// Release releases the advisory lock and closes the underlying connection.
func (l *AdvisoryLock) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.held {
		return nil
	}
	l.held = false

	if l.conn == nil {
		return nil
	}
	defer l.conn.Close()

	var unlocked bool
	if err := l.conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", l.key).Scan(&unlocked); err != nil {
		return fmt.Errorf("pg_advisory_unlock: %w", err)
	}
	return nil
}
