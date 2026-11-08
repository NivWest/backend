package testutil

import (
	"context"
	"sort"
	"sync"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
)

var _ domain.SnapshotRepository = (*SnapshotRepo)(nil)

// ---------------------------------------------------------------------------
// SnapshotRepository (story NW-10)
// ---------------------------------------------------------------------------

// SnapshotRepo is a fake domain.SnapshotRepository keeping snapshots in memory.
//
// Defaults: InsertBatch appends the snapshots (assigning ids) and records the batch size; ListByUser returns
// the user's snapshots with t >= since, oldest first; UserIDsWithOrders returns UsersWithOrders.
type SnapshotRepo struct {
	mu              sync.Mutex
	Snapshots       []models.EquitySnapshot
	Batches         []int
	UsersWithOrders []uint

	InsertBatchFn       func(ctx context.Context, snapshots []models.EquitySnapshot) error
	ListByUserFn        func(ctx context.Context, userID uint, since time.Time) ([]models.EquitySnapshot, error)
	UserIDsWithOrdersFn func(ctx context.Context) ([]uint, error)
}

func (r *SnapshotRepo) InsertBatch(ctx context.Context, snapshots []models.EquitySnapshot) error {
	if r.InsertBatchFn != nil {
		return r.InsertBatchFn(ctx, snapshots)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(snapshots) == 0 {
		return nil
	}
	r.Batches = append(r.Batches, len(snapshots))
	for _, s := range snapshots {
		s.ID = uint(len(r.Snapshots) + 1)
		r.Snapshots = append(r.Snapshots, s)
	}
	return nil
}

func (r *SnapshotRepo) ListByUser(ctx context.Context, userID uint, since time.Time) ([]models.EquitySnapshot, error) {
	if r.ListByUserFn != nil {
		return r.ListByUserFn(ctx, userID, since)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []models.EquitySnapshot{}
	for _, s := range r.Snapshots {
		if s.UserID == userID && (since.IsZero() || !s.T.Before(since)) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].T.Equal(out[j].T) {
			return out[i].T.Before(out[j].T)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *SnapshotRepo) UserIDsWithOrders(ctx context.Context) ([]uint, error) {
	if r.UserIDsWithOrdersFn != nil {
		return r.UserIDsWithOrdersFn(ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]uint{}, r.UsersWithOrders...), nil
}

// ForUser returns a copy of the stored snapshots of userID in insertion order.
func (r *SnapshotRepo) ForUser(userID uint) []models.EquitySnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []models.EquitySnapshot{}
	for _, s := range r.Snapshots {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out
}

// FillObserver is a fake domain.FillObserver recording the user of every fill.
type FillObserver struct {
	mu    sync.Mutex
	Users []uint
}

func (o *FillObserver) OrderFilled(_ context.Context, userID uint) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.Users = append(o.Users, userID)
}

// Calls returns a copy of the recorded user ids.
func (o *FillObserver) Calls() []uint {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]uint{}, o.Users...)
}
