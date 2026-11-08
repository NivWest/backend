//go:build integration

// Integration tests for the equity snapshot repository against a real Postgres (story NW-10): batched inserts,
// per-user reads in time order with a lower bound, the users the periodic snapshot covers, and the guest purge.
// Run like order_integration_test.go.
package repository_test

import (
	"context"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/repository"
)

func TestSnapshotRepo_InsertListAndUsers(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewSnapshotRepository(db)
	ctx := context.Background()
	a := createUser(t, db, "100000")
	b := createUser(t, db, "100000")
	idle := createUser(t, db, "100000")

	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	snap := func(userID uint, h int, equity string) models.EquitySnapshot {
		return models.EquitySnapshot{UserID: userID, T: base.Add(time.Duration(h) * time.Hour),
			Cash: dec(equity), HoldingsValue: dec("0"), VaultValue: dec("0"), Equity: dec(equity)}
	}
	// Inserted out of order: reads must sort by t.
	batch := []models.EquitySnapshot{snap(a.ID, 2, "100200"), snap(a.ID, 0, "100000"), snap(b.ID, 1, "99000"), snap(a.ID, 1, "100100")}
	if err := repo.InsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertBatch(ctx, nil); err != nil {
		t.Fatalf("empty InsertBatch: %v", err)
	}

	all, err := repo.ListByUser(ctx, a.ID, time.Time{})
	if err != nil || len(all) != 3 {
		t.Fatalf("ListByUser(all) = %v, %v", all, err)
	}
	for i, want := range []string{"100000", "100100", "100200"} {
		if !all[i].Equity.Equal(dec(want)) {
			t.Errorf("[%d] equity = %s, want %s", i, all[i].Equity, want)
		}
	}
	since, err := repo.ListByUser(ctx, a.ID, base.Add(time.Hour))
	if err != nil || len(since) != 2 || !since[0].T.Equal(base.Add(time.Hour)) {
		t.Fatalf("ListByUser(since) = %v, %v", since, err)
	}
	none, err := repo.ListByUser(ctx, idle.ID, time.Time{})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("ListByUser(idle) = %#v, %v; want empty non-nil", none, err)
	}

	// Only users with an order are covered by the periodic snapshot.
	ordersRepo := repository.NewOrderRepository(db)
	stockID, _ := ordersRepo.EnsureStock(ctx, "SIM-FJRD", "FJRD", "Fjord Robotics")
	for _, u := range []models.User{b, a} {
		if _, _, err := ordersRepo.Execute(ctx, buy(u.ID, stockID, "1", "100.10", "1.00", "101.10", "")); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := repo.UserIDsWithOrders(ctx)
	if err != nil || len(ids) != 2 || ids[0] != a.ID || ids[1] != b.ID {
		t.Fatalf("UserIDsWithOrders = %v, %v; want [%d %d]", ids, err, a.ID, b.ID)
	}
	if err := db.Delete(&models.User{}, b.ID).Error; err != nil { // soft delete
		t.Fatal(err)
	}
	if ids, _ := repo.UserIDsWithOrders(ctx); len(ids) != 1 || ids[0] != a.ID {
		t.Errorf("after soft-deleting b: %v, want [%d]", ids, a.ID)
	}
}

func TestGuestPurge_DeletesSnapshots(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	guest := createUser(t, db, "100000")
	if err := repository.NewSnapshotRepository(db).InsertBatch(ctx, []models.EquitySnapshot{{
		UserID: guest.ID, T: time.Now().UTC(), Cash: dec("100000"), HoldingsValue: dec("0"), VaultValue: dec("0"), Equity: dec("100000"),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewGuestRepository(db).DeleteExpiredGuests(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.EquitySnapshot{}).Where("user_id = ?", guest.ID).Count(&n)
	if n != 0 {
		t.Errorf("%d snapshots left after the purge, want 0 (role %s)", n, domain.RoleGuest)
	}
}
