//go:build integration

// Integration tests for the order repository against a real Postgres (row locks, unique indexes and
// migrations cannot be faked). Run with:
//
//	TEST_DATABASE_URL='postgres://postgres:pg@localhost:55432/postgres?sslmode=disable' \
//	  go test -tags integration ./internal/repository/...
//
// Each test gets its own fresh schema, dropped afterwards.
package repository_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"backend/internal/domain"
	"backend/internal/integrations/database"
	"backend/internal/models"
	"backend/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const concurrentOrders = 8

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// openSchemaDB connects to TEST_DATABASE_URL with search_path set to a new, empty schema.
func openSchemaDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}

	admin, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	schema := "it_" + hex.EncodeToString(buf)
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}

	scoped := dsn
	if strings.Contains(dsn, "://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		scoped += sep + "search_path=" + schema
	} else {
		scoped += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(scoped), cfg)
	if err != nil {
		t.Fatalf("connect to schema: %v", err)
	}

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func migratedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openSchemaDB(t)
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func createUser(t *testing.T, db *gorm.DB, balance string) models.User {
	t.Helper()
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	id := hex.EncodeToString(buf)
	u := models.User{GoogleID: "it:" + id, Email: id + "@example.invalid", Name: "IT", Role: domain.RoleGuest, Balance: dec(balance)}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func balanceOf(t *testing.T, db *gorm.DB, userID uint) decimal.Decimal {
	t.Helper()
	var u models.User
	if err := db.Take(&u, userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return u.Balance
}

func countOrders(t *testing.T, db *gorm.DB, userID uint) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&models.Order{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		t.Fatalf("count orders: %v", err)
	}
	return n
}

func buy(userID, stockID uint, qty, price, commission, total, key string) domain.ExecuteOrderParams {
	return domain.ExecuteOrderParams{
		UserID: userID, StockID: stockID, Side: domain.OrderSideBuy, Type: domain.OrderTypeMarket,
		Quantity: dec(qty), FillPrice: dec(price), Commission: dec(commission), Total: dec(total),
		IdempotencyKey: key,
	}
}

func sell(userID, stockID uint, qty, price, commission, total string) domain.ExecuteOrderParams {
	return domain.ExecuteOrderParams{
		UserID: userID, StockID: stockID, Side: domain.OrderSideSell, Type: domain.OrderTypeMarket,
		Quantity: dec(qty), FillPrice: dec(price), Commission: dec(commission), Total: dec(total),
	}
}

// assertSameOrder fails unless got deep-equals want and both marshal to byte-identical JSON.
func assertSameOrder(t *testing.T, label string, want, got *domain.Order) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s: order differs\n got  %+v\n want %+v", label, got, want)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("%s: JSON differs\n got  %s\n want %s", label, gotJSON, wantJSON)
	}
}

func TestOrderRepo_ConcurrentBuysNeverOverdraw(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewOrderRepository(db)
	ctx := context.Background()

	user := createUser(t, db, "100000.00")
	stockID, err := repo.EnsureStock(ctx, "SIM-FJRD", "FJRD", "Fjord Industries")
	if err != nil {
		t.Fatalf("ensure stock: %v", err)
	}

	// 200 x 100.10 = 20020.00 + commission 150.30 = 20170.30; 100000 covers exactly 4.
	var (
		wg               sync.WaitGroup
		mu               sync.Mutex
		ok, insufficient int
		unexpected       []error
	)
	for range concurrentOrders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			order, created, err := repo.Execute(ctx, buy(user.ID, stockID, "200", "100.10", "150.30", "20170.30", ""))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && created && order != nil:
				ok++
			case errors.Is(err, domain.ErrInsufficientFunds):
				insufficient++
			default:
				unexpected = append(unexpected, fmt.Errorf("created=%v err=%v", created, err))
			}
		}()
	}
	wg.Wait()

	if len(unexpected) > 0 {
		t.Fatalf("unexpected results: %v", unexpected)
	}
	if ok != 4 || insufficient != 4 {
		t.Fatalf("got %d filled / %d insufficient, want 4 / 4", ok, insufficient)
	}
	want := dec("100000").Sub(dec("20170.30").Mul(decimal.NewFromInt(4)))
	if got := balanceOf(t, db, user.ID); !got.Equal(want) {
		t.Fatalf("balance = %s, want %s", got, want)
	}
	if n := countOrders(t, db, user.ID); n != 4 {
		t.Fatalf("orders = %d, want 4", n)
	}
	var pos models.Position
	if err := db.Where("user_id = ? AND stock_id = ?", user.ID, stockID).Take(&pos).Error; err != nil {
		t.Fatalf("load position: %v", err)
	}
	if !pos.Quantity.Equal(dec("800")) || !pos.AvgPrice.Equal(dec("100.10")) {
		t.Fatalf("position = %s @ %s, want 800 @ 100.10", pos.Quantity, pos.AvgPrice)
	}
}

func TestOrderRepo_ExecuteReturnsFilledOrder(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewOrderRepository(db)
	ctx := context.Background()

	user := createUser(t, db, "100000.00")
	stockID, err := repo.EnsureStock(ctx, "SIM-FJRD", "FJRD", "Fjord Industries")
	if err != nil {
		t.Fatal(err)
	}

	order, created, err := repo.Execute(ctx, buy(user.ID, stockID, "10", "100.10", "2.50", "1003.50", ""))
	if err != nil || !created {
		t.Fatalf("execute: created=%v err=%v", created, err)
	}
	if order.ID == 0 || order.OrderbookID != "SIM-FJRD" || order.Symbol != "FJRD" || order.Name != "Fjord Industries" ||
		order.Side != domain.OrderSideBuy || order.Type != domain.OrderTypeMarket || order.Status != domain.OrderStatusFilled ||
		!order.Quantity.Equal(dec("10")) || !order.FillPrice.Equal(dec("100.10")) ||
		!order.Commission.Equal(dec("2.50")) || !order.Total.Equal(dec("1003.50")) || order.FilledAt == nil {
		t.Fatalf("unexpected order: %+v", order)
	}
	if got := balanceOf(t, db, user.ID); !got.Equal(dec("98996.50")) {
		t.Fatalf("balance = %s, want 98996.50", got)
	}

	// Weighted average: (10*100.10 + 10*99.95) / 20 = 100.025 -> 100.03 (half-up).
	if _, _, err := repo.Execute(ctx, buy(user.ID, stockID, "10", "99.95", "2.50", "1002.00", "")); err != nil {
		t.Fatal(err)
	}
	var pos models.Position
	if err := db.Where("user_id = ? AND stock_id = ?", user.ID, stockID).Take(&pos).Error; err != nil {
		t.Fatal(err)
	}
	if !pos.Quantity.Equal(dec("20")) || !pos.AvgPrice.Equal(dec("100.03")) {
		t.Fatalf("position = %s @ %s, want 20 @ 100.03", pos.Quantity, pos.AvgPrice)
	}

	if _, _, err := repo.Execute(ctx, buy(9999999, stockID, "1", "1", "0", "1", "")); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("unknown user: err = %v, want ErrUserNotFound", err)
	}
}

func TestOrderRepo_IdempotencyKey(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewOrderRepository(db)
	ctx := context.Background()

	stockID, err := repo.EnsureStock(ctx, "SIM-FJRD", "FJRD", "Fjord Industries")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("sequential retry", func(t *testing.T) {
		user := createUser(t, db, "100000.00")
		if _, err := repo.FindByIdempotencyKey(ctx, user.ID, "k-123"); !errors.Is(err, domain.ErrOrderNotFound) {
			t.Fatalf("before first order: err = %v, want ErrOrderNotFound", err)
		}
		p := buy(user.ID, stockID, "10", "100.10", "2.50", "1003.50", "k-123")
		first, created, err := repo.Execute(ctx, p)
		if err != nil || !created {
			t.Fatalf("first: created=%v err=%v", created, err)
		}
		second, created, err := repo.Execute(ctx, p)
		if err != nil || created {
			t.Fatalf("second: created=%v err=%v, want created=false", created, err)
		}
		if second.ID != first.ID || second.OrderbookID != "SIM-FJRD" || !second.Total.Equal(first.Total) {
			t.Fatalf("second = %+v, want order %d", second, first.ID)
		}
		found, err := repo.FindByIdempotencyKey(ctx, user.ID, "k-123")
		if err != nil || found.ID != first.ID {
			t.Fatalf("find: %+v, %v", found, err)
		}
		// The created order, the replay and a later read are the same order, timestamps included
		// (Postgres keeps microseconds; a nanosecond CreatedAt on the created path broke AC4).
		if first.FilledAt == nil || first.CreatedAt.Nanosecond()%1000 != 0 {
			t.Fatalf("created order timestamps not at microsecond precision: %+v", first)
		}
		assertSameOrder(t, "replay", first, second)
		assertSameOrder(t, "find", first, found)

		if n := countOrders(t, db, user.ID); n != 1 {
			t.Fatalf("orders = %d, want 1", n)
		}
		if got := balanceOf(t, db, user.ID); !got.Equal(dec("98996.50")) {
			t.Fatalf("balance = %s, want 98996.50 (debited once)", got)
		}

		// The key is per user: another user may use it too.
		other := createUser(t, db, "100000.00")
		if _, created, err := repo.Execute(ctx, buy(other.ID, stockID, "1", "100.10", "0", "100.10", "k-123")); err != nil || !created {
			t.Fatalf("other user: created=%v err=%v", created, err)
		}
	})

	t.Run("concurrent retries", func(t *testing.T) {
		user := createUser(t, db, "100000.00")
		p := buy(user.ID, stockID, "10", "100.10", "2.50", "1003.50", "k-concurrent")
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			ids     = map[uint]int{}
			created int
			errs    []error
		)
		for range concurrentOrders {
			wg.Add(1)
			go func() {
				defer wg.Done()
				order, c, err := repo.Execute(ctx, p)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, err)
					return
				}
				ids[order.ID]++
				if c {
					created++
				}
			}()
		}
		wg.Wait()
		if len(errs) > 0 {
			t.Fatalf("errors: %v", errs)
		}
		if created != 1 || len(ids) != 1 {
			t.Fatalf("created=%d distinct ids=%v, want 1 and 1", created, ids)
		}
		if n := countOrders(t, db, user.ID); n != 1 {
			t.Fatalf("orders = %d, want 1", n)
		}
		if got := balanceOf(t, db, user.ID); !got.Equal(dec("98996.50")) {
			t.Fatalf("balance = %s, want 98996.50 (debited once)", got)
		}
	})
}

func TestOrderRepo_Sell(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewOrderRepository(db)
	ctx := context.Background()

	user := createUser(t, db, "100000.00")
	stockID, err := repo.EnsureStock(ctx, "SIM-FJRD", "FJRD", "Fjord Industries")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := repo.Execute(ctx, sell(user.ID, stockID, "1", "99.90", "1.00", "98.90")); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("sell without position: err = %v, want ErrInsufficientShares", err)
	}
	if _, _, err := repo.Execute(ctx, buy(user.ID, stockID, "10", "100.10", "2.50", "1003.50", "")); err != nil {
		t.Fatal(err)
	}
	order, created, err := repo.Execute(ctx, sell(user.ID, stockID, "4", "99.90", "1.00", "398.60"))
	if err != nil || !created || order.Side != domain.OrderSideSell {
		t.Fatalf("sell 4: %+v created=%v err=%v", order, created, err)
	}
	if got := balanceOf(t, db, user.ID); !got.Equal(dec("99395.10")) {
		t.Fatalf("balance = %s, want 99395.10", got)
	}

	before := balanceOf(t, db, user.ID)
	if _, _, err := repo.Execute(ctx, sell(user.ID, stockID, "50", "99.90", "5.00", "4990.00")); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Fatalf("sell 50 holding 6: err = %v, want ErrInsufficientShares", err)
	}
	if got := balanceOf(t, db, user.ID); !got.Equal(before) {
		t.Fatalf("balance changed on rejected sell: %s -> %s", before, got)
	}

	var pos models.Position
	if err := db.Where("user_id = ? AND stock_id = ?", user.ID, stockID).Take(&pos).Error; err != nil {
		t.Fatal(err)
	}
	if !pos.Quantity.Equal(dec("6")) || !pos.AvgPrice.Equal(dec("100.10")) {
		t.Fatalf("position = %s @ %s, want 6 @ 100.10", pos.Quantity, pos.AvgPrice)
	}

	if _, _, err := repo.Execute(ctx, sell(user.ID, stockID, "6", "99.90", "1.00", "598.40")); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Position{}).Where("user_id = ?", user.ID).Count(&n)
	if n != 0 {
		t.Fatalf("positions = %d after selling everything, want 0", n)
	}
	if n := countOrders(t, db, user.ID); n != 3 {
		t.Fatalf("orders = %d, want 3", n)
	}
}

func TestOrderRepo_EnsureStockConcurrent(t *testing.T) {
	db := migratedDB(t)
	repo := repository.NewOrderRepository(db)
	ctx := context.Background()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = map[uint]int{}
		errs []error
	)
	for range concurrentOrders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := repo.EnsureStock(ctx, "SIM-NOVA", "NOVA", "Nova Robotics")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			ids[id]++
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(ids) != 1 {
		t.Fatalf("distinct ids = %v, want one", ids)
	}
	var n int64
	db.Model(&models.Stock{}).Where("orderbook_id = ?", "SIM-NOVA").Count(&n)
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}

	// A soft-deleted row still counts as existing.
	var id uint
	for k := range ids {
		id = k
	}
	if err := db.Delete(&models.Stock{}, id).Error; err != nil {
		t.Fatal(err)
	}
	again, err := repo.EnsureStock(ctx, "SIM-NOVA", "NOVA", "Nova Robotics")
	if err != nil || again != id {
		t.Fatalf("after soft delete: id=%d err=%v, want %d", again, err, id)
	}
}

func TestWatchlistRepo_EnsureStockBySymbol(t *testing.T) {
	db := migratedDB(t)
	orders := repository.NewOrderRepository(db)
	watchlists := repository.NewWatchlistRepository(db)
	ctx := context.Background()

	id, err := orders.EnsureStock(ctx, "5247", "VOLV B", "Volvo B")
	if err != nil {
		t.Fatal(err)
	}
	existing, err := watchlists.EnsureStock(ctx, "VOLV B", "Volvo B")
	if err != nil || existing.ID != id {
		t.Fatalf("existing symbol: %+v %v, want id %d", existing, err, id)
	}
	created, err := watchlists.EnsureStock(ctx, "ERIC B", "Ericsson B")
	if err != nil || created.ID == 0 || created.OrderbookID != "ERIC B" || created.Symbol != "ERIC B" {
		t.Fatalf("new symbol: %+v %v", created, err)
	}
}

func TestMigrate_LegacySchema(t *testing.T) {
	db := openSchemaDB(t)

	// The pre-NW-04 schema: stocks keyed by a unique symbol, float-era numeric(15,x) columns.
	legacy := []string{
		`CREATE TABLE users (
			id bigserial PRIMARY KEY, google_id text NOT NULL, email text NOT NULL, name text,
			role varchar(20) NOT NULL DEFAULT 'member', balance numeric(15,2) DEFAULT 100000.00,
			created_at timestamptz, updated_at timestamptz, deleted_at timestamptz)`,
		`CREATE UNIQUE INDEX idx_users_google_id ON users (google_id)`,
		`CREATE UNIQUE INDEX idx_users_email ON users (email)`,
		`CREATE TABLE stocks (
			id bigserial PRIMARY KEY, symbol text NOT NULL, name text,
			created_at timestamptz, updated_at timestamptz, deleted_at timestamptz)`,
		`CREATE UNIQUE INDEX idx_stocks_symbol ON stocks (symbol)`,
		`CREATE TABLE positions (
			id bigserial PRIMARY KEY, user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			stock_id bigint NOT NULL REFERENCES stocks(id) ON DELETE CASCADE,
			quantity numeric(15,4) NOT NULL, avg_price numeric(15,2) NOT NULL,
			created_at timestamptz, updated_at timestamptz)`,
		`INSERT INTO users (google_id, email, name, balance) VALUES ('g1', 'a@example.invalid', 'A', 95000.50)`,
		`INSERT INTO stocks (symbol, name) VALUES ('VOLV B', 'Volvo B'), ('ERIC B', 'Ericsson B')`,
		`INSERT INTO positions (user_id, stock_id, quantity, avg_price) VALUES (1, 1, 12.5, 250.10)`,
	}
	for _, stmt := range legacy {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("legacy schema: %v\n%s", err, stmt)
		}
	}

	for i := range 2 { // the second run proves the migration is idempotent
		if err := database.Migrate(db); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}

	var stocks []models.Stock
	if err := db.Order("id").Find(&stocks).Error; err != nil {
		t.Fatal(err)
	}
	if len(stocks) != 2 || stocks[0].OrderbookID != "VOLV B" || stocks[1].OrderbookID != "ERIC B" {
		t.Fatalf("stocks not backfilled: %+v", stocks)
	}

	type column struct {
		IsNullable       string
		NumericPrecision *int
		NumericScale     *int
	}
	checkColumn := func(table, name string, nullable string, precision, scale int) {
		t.Helper()
		var c column
		if err := db.Raw(`SELECT is_nullable, numeric_precision, numeric_scale FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`, table, name).Scan(&c).Error; err != nil {
			t.Fatal(err)
		}
		if c.IsNullable != nullable {
			t.Errorf("%s.%s nullable = %q, want %q", table, name, c.IsNullable, nullable)
		}
		if precision > 0 && (c.NumericPrecision == nil || *c.NumericPrecision != precision || c.NumericScale == nil || *c.NumericScale != scale) {
			t.Errorf("%s.%s = numeric(%v,%v), want numeric(%d,%d)", table, name, c.NumericPrecision, c.NumericScale, precision, scale)
		}
	}
	checkColumn("stocks", "orderbook_id", "NO", 0, 0)
	checkColumn("users", "balance", "NO", 18, 2)
	checkColumn("positions", "quantity", "NO", 18, 4)
	checkColumn("positions", "avg_price", "NO", 18, 2)

	var indexes []string
	if err := db.Raw(`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'stocks'`).
		Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(indexes, ",")
	if strings.Contains(","+joined+",", ",idx_stocks_symbol,") {
		t.Errorf("old unique idx_stocks_symbol still present: %s", joined)
	}
	for _, want := range []string{"idx_stocks_orderbook_id", "idx_stocks_symbol_lookup"} {
		if !strings.Contains(joined, want) {
			t.Errorf("index %s missing: %s", want, joined)
		}
	}

	// Two markets may now share a symbol.
	repo := repository.NewOrderRepository(db)
	if _, err := repo.EnsureStock(context.Background(), "SIM-VOLV", "VOLV B", "Volvo B (sim)"); err != nil {
		t.Fatalf("duplicate symbol after migrate: %v", err)
	}
	if got := balanceOf(t, db, 1); !got.Equal(dec("95000.50")) {
		t.Fatalf("legacy balance = %s, want 95000.50", got)
	}
}
