//go:build integration

// Integration tests for the watchlist repository against a real Postgres (story NW-06): owner scoping, the
// unique (watchlist_id, stock_id) index, the per-user list limit under concurrency and the legacy dedupe.
// Run like order_integration_test.go.
package repository_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"backend/internal/domain"
	"backend/internal/integrations/database"
	"backend/internal/models"
	"backend/internal/repository"
)

func TestWatchlistRepo_OwnerScopedCRUD(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewWatchlistRepository(db)
	ctx := context.Background()
	owner := createUser(t, db, "100000")
	other := createUser(t, db, "100000")

	wl, err := repo.Create(ctx, owner.ID, "Energy", domain.MaxWatchlists)
	if err != nil || wl.ID == 0 || wl.Name != "Energy" || len(wl.Items) != 0 || wl.CreatedAt.IsZero() {
		t.Fatalf("Create() = %+v, %v", wl, err)
	}
	nve, err := repo.EnsureStock(ctx, "SIM-NVE", "NVE", "Nordvik Energy")
	if err != nil {
		t.Fatal(err)
	}
	fjrd, _ := repo.EnsureStock(ctx, "SIM-FJRD", "FJRD", "Fjord Robotics")

	item, err := repo.AddItem(ctx, owner.ID, wl.ID, nve)
	if err != nil || item.OrderbookID != "SIM-NVE" || item.Symbol != "NVE" || item.Name != "Nordvik Energy" || item.AddedAt.IsZero() {
		t.Fatalf("AddItem() = %+v, %v", item, err)
	}
	if _, err := repo.AddItem(ctx, owner.ID, wl.ID, nve); !errors.Is(err, domain.ErrDuplicateItem) {
		t.Fatalf("duplicate AddItem error = %v, want ErrDuplicateItem", err)
	}
	if _, err := repo.AddItem(ctx, owner.ID, wl.ID, fjrd); err != nil {
		t.Fatal(err)
	}

	// Another user sees nothing and can change nothing.
	if _, err := repo.Get(ctx, other.ID, wl.ID); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("other Get error = %v", err)
	}
	if err := repo.Rename(ctx, other.ID, wl.ID, "Mine"); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("other Rename error = %v", err)
	}
	if _, err := repo.AddItem(ctx, other.ID, wl.ID, fjrd); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("other AddItem error = %v", err)
	}
	if err := repo.RemoveItem(ctx, other.ID, wl.ID, "SIM-NVE"); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("other RemoveItem error = %v", err)
	}
	if err := repo.Delete(ctx, other.ID, wl.ID); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("other Delete error = %v", err)
	}
	if lists, err := repo.List(ctx, other.ID); err != nil || len(lists) != 0 {
		t.Errorf("other List = %+v, %v", lists, err)
	}

	got, err := repo.Get(ctx, owner.ID, wl.ID)
	if err != nil || len(got.Items) != 2 || got.Items[0].OrderbookID != "SIM-NVE" || got.Items[1].OrderbookID != "SIM-FJRD" {
		t.Fatalf("Get() = %+v, %v; want SIM-NVE then SIM-FJRD", got, err)
	}

	if err := repo.Rename(ctx, owner.ID, wl.ID, "Renewables"); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveItem(ctx, owner.ID, wl.ID, "SIM-NVE"); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveItem(ctx, owner.ID, wl.ID, "SIM-NVE"); !errors.Is(err, domain.ErrWatchlistItemNotFound) {
		t.Errorf("second RemoveItem error = %v, want ErrWatchlistItemNotFound", err)
	}
	lists, err := repo.List(ctx, owner.ID)
	if err != nil || len(lists) != 1 || lists[0].Name != "Renewables" || len(lists[0].Items) != 1 {
		t.Fatalf("List() = %+v, %v", lists, err)
	}

	if err := repo.Delete(ctx, owner.ID, wl.ID); err != nil {
		t.Fatal(err)
	}
	var orphans int64
	db.Model(&models.WatchlistItem{}).Where("watchlist_id = ?", wl.ID).Count(&orphans)
	if orphans != 0 {
		t.Errorf("%d items left after deleting the list", orphans)
	}
	if _, err := repo.Get(ctx, owner.ID, wl.ID); !errors.Is(err, domain.ErrWatchlistNotFound) {
		t.Errorf("Get after Delete error = %v", err)
	}
}

func TestWatchlistRepo_ConcurrentCreatesRespectTheLimit(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewWatchlistRepository(db)
	user := createUser(t, db, "100000")
	ctx := context.Background()

	const attempts = 16
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.Create(ctx, user.ID, "L", domain.MaxWatchlists)
		}()
	}
	wg.Wait()

	created, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, domain.ErrWatchlistLimit):
			limited++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if created != domain.MaxWatchlists || limited != attempts-domain.MaxWatchlists {
		t.Fatalf("created %d, limited %d; want %d and %d", created, limited, domain.MaxWatchlists, attempts-domain.MaxWatchlists)
	}
}

func TestWatchlistRepo_ConcurrentDuplicateAddsInsertOnce(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewWatchlistRepository(db)
	user := createUser(t, db, "100000")
	ctx := context.Background()
	wl, _ := repo.Create(ctx, user.ID, "Energy", domain.MaxWatchlists)
	nve, _ := repo.EnsureStock(ctx, "SIM-NVE", "NVE", "Nordvik Energy")

	const attempts = 8
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.AddItem(ctx, user.ID, wl.ID, nve)
		}()
	}
	wg.Wait()
	ok, dup := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrDuplicateItem):
			dup++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || dup != attempts-1 {
		t.Fatalf("added %d, duplicates %d; want 1 and %d", ok, dup, attempts-1)
	}
}

func TestMigrate_DedupesLegacyWatchlistItems(t *testing.T) {
	db := openSchemaDB(t)
	legacy := []string{
		`CREATE TABLE stocks (id bigserial PRIMARY KEY, symbol text NOT NULL, name text,
			created_at timestamptz, updated_at timestamptz, deleted_at timestamptz)`,
		`INSERT INTO stocks (symbol, name) VALUES ('NVE', 'Nordvik Energy'), ('FJRD', 'Fjord Robotics')`,
		`CREATE TABLE watchlists (id bigserial PRIMARY KEY, user_id bigint NOT NULL, name varchar(100) NOT NULL,
			created_at timestamptz, updated_at timestamptz)`,
		`INSERT INTO watchlists (user_id, name) VALUES (1, 'A'), (1, 'B')`,
		`CREATE TABLE watchlist_items (id bigserial PRIMARY KEY,
			watchlist_id bigint NOT NULL REFERENCES watchlists(id) ON DELETE CASCADE,
			stock_id bigint NOT NULL REFERENCES stocks(id) ON DELETE CASCADE, created_at timestamptz)`,
		`INSERT INTO watchlist_items (watchlist_id, stock_id) VALUES (1, 1), (1, 1), (1, 2), (2, 1), (1, 1)`,
	}
	for _, stmt := range legacy {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("legacy schema: %v\n%s", err, stmt)
		}
	}
	for i := range 2 {
		if err := database.Migrate(db); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}
	var ids []uint
	if err := db.Raw(`SELECT id FROM watchlist_items ORDER BY id`).Scan(&ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 3 || ids[2] != 4 {
		t.Fatalf("items after dedupe = %v, want [1 3 4]", ids)
	}
	if err := db.Exec(`INSERT INTO watchlist_items (watchlist_id, stock_id) VALUES (1, 2)`).Error; err == nil {
		t.Fatal("unique index (watchlist_id, stock_id) missing: duplicate insert succeeded")
	}
}
