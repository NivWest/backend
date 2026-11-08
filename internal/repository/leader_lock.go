package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"

	"backend/internal/domain"
	"gorm.io/gorm"
)

// advisoryLock is a domain.LeaderLock over a Postgres session advisory lock (story NW-08). The lock belongs to
// the database session that took it, so it is held on one dedicated connection taken out of the pool: it lasts
// while that connection lives and is released by pg_advisory_unlock, by closing the connection, or by the
// server when the process dies. Replicas that do not get it simply try again on the next interval.
type advisoryLock struct {
	db  *sql.DB
	key int64

	mu   sync.Mutex
	conn *sql.Conn // non-nil while held
}

// NewAdvisoryLock returns a leader lock on key (domain.MatchingEngineLockKey for the matching engine).
func NewAdvisoryLock(db *gorm.DB, key int64) (domain.LeaderLock, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("advisory lock: %w", err)
	}
	return &advisoryLock{db: sqlDB, key: key}, nil
}

// TryAcquire implements domain.LeaderLock. A held lock is re-verified with a ping on its connection; when the
// connection is gone, so is the lock, and it is retried on a fresh connection.
func (l *advisoryLock) TryAcquire(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.conn != nil {
		if err := l.conn.PingContext(ctx); err == nil {
			return true, nil
		}
		discardConn(l.conn)
		l.conn = nil
	}

	conn, err := l.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("advisory lock connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&acquired); err != nil {
		discardConn(conn)
		return false, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		_ = conn.Close() // holds nothing: back to the pool
		return false, nil
	}
	l.conn = conn
	return true, nil
}

// Release implements domain.LeaderLock.
func (l *advisoryLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil
	var released bool
	if err := conn.QueryRowContext(context.Background(), "SELECT pg_advisory_unlock($1)", l.key).Scan(&released); err != nil {
		// Never return a connection that may still hold the lock to the pool.
		discardConn(conn)
		return fmt.Errorf("pg_advisory_unlock: %w", err)
	}
	return conn.Close()
}

// discardConn closes conn's driver connection instead of returning it to the pool (database/sql drops a
// connection whose Raw callback answers driver.ErrBadConn), so a session that may hold the lock ends.
func discardConn(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}
