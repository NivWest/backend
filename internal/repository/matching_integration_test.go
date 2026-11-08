//go:build integration

// Integration tests for pending orders, the matching repository and the advisory leader lock (story NW-08) against
// a real Postgres: reservations, FOR UPDATE serialization of cancel vs fill, and session advisory locks cannot be
// faked. Run like order_integration_test.go (TEST_DATABASE_URL, -tags integration).
package repository_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/repository"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func pdec(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

func userRow(t *testing.T, db *gorm.DB, id uint) models.User {
	t.Helper()
	var u models.User
	if err := db.Take(&u, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	return u
}

func expectDec(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Fatalf("%s = %s, want %s", what, got, want)
	}
}

// buyLimit10at95 is AC1: 10 × 95 reserves 952.38.
func buyLimit10at95(userID, stockID uint) domain.PendingOrderParams {
	return domain.PendingOrderParams{UserID: userID, StockID: stockID, Side: domain.OrderSideBuy, Type: domain.OrderTypeLimit,
		Quantity: dec("10"), LimitPrice: pdec("95"), ReservedCash: dec("952.38")}
}

func setupKall(t *testing.T) (*gorm.DB, domain.OrderRepository, domain.MatchingRepository, uint) {
	t.Helper()
	db := migratedDB(t)
	orders := repository.NewOrderRepository(db)
	stockID, err := orders.EnsureStock(context.Background(), "SIM-KALL", "KALL", "Kallsten Bank")
	if err != nil {
		t.Fatal(err)
	}
	return db, orders, repository.NewMatchingRepository(db), stockID
}

func TestPendingOrders_BuyLimitReservesAndFills(t *testing.T) {
	db, orders, matching, stockID := setupKall(t)
	ctx := context.Background()
	user := createUser(t, db, "100000.00")

	placed, created, err := orders.PlacePending(ctx, buyLimit10at95(user.ID, stockID))
	if err != nil || !created {
		t.Fatalf("PlacePending() = %v, %v", created, err)
	}
	if placed.Status != domain.OrderStatusOpen || placed.LimitPrice == nil || !placed.LimitPrice.Equal(dec("95")) || placed.StopPrice != nil {
		t.Fatalf("placed = %+v", placed)
	}
	u := userRow(t, db, user.ID)
	expectDec(t, "balance after placing", u.Balance, "100000.00")
	expectDec(t, "reserved_cash after placing (AC1)", u.ReservedCash, "952.38")

	open, err := matching.ListOpen(ctx)
	if err != nil || len(open) != 1 || open[0].ID != placed.ID || open[0].OrderbookID != "SIM-KALL" || open[0].UserID != user.ID {
		t.Fatalf("ListOpen() = %+v, %v", open, err)
	}

	filledAt := time.Date(2026, 11, 8, 9, 41, 3, 0, time.UTC)
	filled, err := matching.FillOrder(ctx, domain.FillOrderParams{OrderID: placed.ID, FillPrice: dec("94.89"),
		Commission: dec("2.37"), Total: dec("951.27"), FilledAt: filledAt})
	if err != nil {
		t.Fatalf("FillOrder() error = %v", err)
	}
	if filled.Status != domain.OrderStatusFilled || !filled.Total.Equal(dec("951.27")) || filled.FilledAt == nil || !filled.FilledAt.Equal(filledAt) {
		t.Fatalf("filled = %+v", filled)
	}
	u = userRow(t, db, user.ID)
	expectDec(t, "balance after fill (AC2)", u.Balance, "99048.73")
	expectDec(t, "reserved_cash after fill (AC2)", u.ReservedCash, "0")
	pos := positionOf(t, db, user.ID, stockID)
	expectDec(t, "position quantity", pos.Quantity, "10")
	expectDec(t, "position avg_cost", pos.AvgCost, "95.127")

	if _, err := matching.FillOrder(ctx, domain.FillOrderParams{OrderID: placed.ID, FillPrice: dec("1"), Total: dec("1")}); !errors.Is(err, domain.ErrOrderNotOpen) {
		t.Errorf("second FillOrder() error = %v, want ErrOrderNotOpen", err)
	}
	if open, _ := matching.ListOpen(ctx); len(open) != 0 {
		t.Errorf("ListOpen() after the fill = %+v, want none", open)
	}
}

func TestPendingOrders_ReservationsLimitBuyingPowerAndShares(t *testing.T) {
	db, orders, _, stockID := setupKall(t)
	ctx := context.Background()
	user := createUser(t, db, "1000.00")

	if _, _, err := orders.PlacePending(ctx, buyLimit10at95(user.ID, stockID)); err != nil {
		t.Fatalf("PlacePending() error = %v", err)
	}
	// Buying power is 1000 − 952.38 = 47.62.
	second := buyLimit10at95(user.ID, stockID)
	second.Quantity, second.ReservedCash = dec("1"), dec("96.24")
	if _, _, err := orders.PlacePending(ctx, second); !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Errorf("second reservation error = %v, want ErrInsufficientFunds (AC3)", err)
	}
	if _, _, err := orders.Execute(ctx, buy(user.ID, stockID, "1", "50.00", "1.00", "51.00", "")); !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Errorf("market buy over buying power error = %v, want ErrInsufficientFunds", err)
	}
	if _, _, err := orders.Execute(ctx, buy(user.ID, stockID, "1", "46.62", "1.00", "47.62", "")); err != nil {
		t.Errorf("market buy of exactly the buying power error = %v", err)
	}

	// Shares: hold 1, reserve it with a SELL LIMIT, then nothing more can be sold.
	sellLimit := domain.PendingOrderParams{UserID: user.ID, StockID: stockID, Side: domain.OrderSideSell, Type: domain.OrderTypeLimit,
		Quantity: dec("2"), LimitPrice: pdec("105")}
	if _, _, err := orders.PlacePending(ctx, sellLimit); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Errorf("sell limit over the position error = %v, want ErrInsufficientShares (AC3)", err)
	}
	sellLimit.Quantity = dec("1")
	if _, _, err := orders.PlacePending(ctx, sellLimit); err != nil {
		t.Fatalf("sell limit of the position error = %v", err)
	}
	expectDec(t, "reserved_quantity", positionOf(t, db, user.ID, stockID).ReservedQuantity, "1")
	if _, _, err := orders.PlacePending(ctx, sellLimit); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Errorf("sell of reserved shares error = %v, want ErrInsufficientShares", err)
	}
	if _, _, err := orders.Execute(ctx, sell(user.ID, stockID, "1", "100", "1", "99")); !errors.Is(err, domain.ErrInsufficientShares) {
		t.Errorf("market sell of reserved shares error = %v, want ErrInsufficientShares", err)
	}
}

func TestPendingOrders_CancelReleasesOnce(t *testing.T) {
	db, orders, _, stockID := setupKall(t)
	ctx := context.Background()
	user := createUser(t, db, "100000.00")
	other := createUser(t, db, "100000.00")

	placed, _, err := orders.PlacePending(ctx, buyLimit10at95(user.ID, stockID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orders.Cancel(ctx, other.ID, placed.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("Cancel(other user) error = %v, want ErrOrderNotFound (AC4)", err)
	}
	if _, err := orders.GetByID(ctx, other.ID, placed.ID); !errors.Is(err, domain.ErrOrderNotFound) {
		t.Errorf("GetByID(other user) error = %v, want ErrOrderNotFound", err)
	}
	cancelled, err := orders.Cancel(ctx, user.ID, placed.ID)
	if err != nil || cancelled.Status != domain.OrderStatusCancelled {
		t.Fatalf("Cancel() = %+v, %v", cancelled, err)
	}
	expectDec(t, "reserved_cash after cancel (AC4)", userRow(t, db, user.ID).ReservedCash, "0")
	if _, err := orders.Cancel(ctx, user.ID, placed.ID); !errors.Is(err, domain.ErrOrderNotOpen) {
		t.Errorf("Cancel(again) error = %v, want ErrOrderNotOpen (AC4)", err)
	}
	expectDec(t, "reserved_cash after second cancel", userRow(t, db, user.ID).ReservedCash, "0")
	got, err := orders.GetByID(ctx, user.ID, placed.ID)
	if err != nil || got.Status != domain.OrderStatusCancelled {
		t.Errorf("GetByID() = %+v, %v", got, err)
	}

	// AC6: the status filter.
	open, _, _ := orders.PlacePending(ctx, buyLimit10at95(user.ID, stockID))
	list, err := orders.ListByUser(ctx, user.ID, nil, 10, domain.OrderStatusOpen)
	if err != nil || len(list) != 1 || list[0].ID != open.ID {
		t.Errorf("ListByUser(OPEN) = %+v, %v; want only %d", list, err, open.ID)
	}
	list, err = orders.ListByUser(ctx, user.ID, nil, 10, domain.OrderStatusFilled, domain.OrderStatusCancelled)
	if err != nil || len(list) != 1 || list[0].ID != placed.ID {
		t.Errorf("ListByUser(history) = %+v, %v; want only %d", list, err, placed.ID)
	}
}

func TestPendingOrders_RejectAtFillTimeReleasesTheReservation(t *testing.T) {
	db, orders, matching, stockID := setupKall(t)
	ctx := context.Background()
	user := createUser(t, db, "100000.00")

	placed, _, err := orders.PlacePending(ctx, buyLimit10at95(user.ID, stockID))
	if err != nil {
		t.Fatal(err)
	}
	// Slippage (or a large fill) can cost more than the reservation; with only 960 cash left it no longer fits.
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Update("balance", dec("960.00")).Error; err != nil {
		t.Fatal(err)
	}
	other, _, err := orders.PlacePending(ctx, domain.PendingOrderParams{UserID: user.ID, StockID: stockID, Side: domain.OrderSideBuy,
		Type: domain.OrderTypeLimit, Quantity: dec("0.01"), LimitPrice: pdec("100"), ReservedCash: dec("2.00")})
	if err != nil {
		t.Fatal(err)
	}
	// Cash outside other reservations: 960 − 2.00 = 958.00 < 1003.50.
	_, err = matching.FillOrder(ctx, domain.FillOrderParams{OrderID: placed.ID, FillPrice: dec("100.10"), Commission: dec("2.50"),
		Total: dec("1003.50"), FilledAt: time.Now()})
	if !errors.Is(err, domain.ErrInsufficientFunds) {
		t.Fatalf("FillOrder() error = %v, want ErrInsufficientFunds", err)
	}
	u := userRow(t, db, user.ID)
	expectDec(t, "balance after failed fill", u.Balance, "960.00")
	expectDec(t, "reserved_cash after failed fill (rolled back)", u.ReservedCash, "954.38")

	rejected, err := matching.RejectOrder(ctx, placed.ID, "insufficient funds at fill time")
	if err != nil || rejected.Status != domain.OrderStatusRejected || rejected.RejectReason == nil || *rejected.RejectReason != "insufficient funds at fill time" {
		t.Fatalf("RejectOrder() = %+v, %v", rejected, err)
	}
	expectDec(t, "reserved_cash after reject", userRow(t, db, user.ID).ReservedCash, "2.00")
	if _, err := matching.RejectOrder(ctx, placed.ID, "again"); !errors.Is(err, domain.ErrOrderNotOpen) {
		t.Errorf("RejectOrder(again) error = %v, want ErrOrderNotOpen", err)
	}
	if _, err := orders.Cancel(ctx, user.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	expectDec(t, "reserved_cash after all", userRow(t, db, user.ID).ReservedCash, "0")
}

func TestPendingOrders_SellStopClosesThePosition(t *testing.T) {
	db, orders, matching, stockID := setupKall(t)
	ctx := context.Background()
	user := createUser(t, db, "100000.00")
	if _, _, err := orders.Execute(ctx, buy(user.ID, stockID, "10", "100.10", "2.50", "1003.50", "")); err != nil {
		t.Fatal(err)
	}
	placed, _, err := orders.PlacePending(ctx, domain.PendingOrderParams{UserID: user.ID, StockID: stockID, Side: domain.OrderSideSell,
		Type: domain.OrderTypeStop, Quantity: dec("10"), StopPrice: pdec("90")})
	if err != nil {
		t.Fatal(err)
	}
	if placed.StopPrice == nil || !placed.StopPrice.Equal(dec("90")) || placed.LimitPrice != nil {
		t.Fatalf("placed = %+v", placed)
	}
	expectDec(t, "reserved_quantity", positionOf(t, db, user.ID, stockID).ReservedQuantity, "10")

	if _, err := matching.FillOrder(ctx, domain.FillOrderParams{OrderID: placed.ID, FillPrice: dec("88.91"), Commission: dec("2.22"),
		Total: dec("886.88"), FilledAt: time.Now()}); err != nil {
		t.Fatalf("FillOrder() error = %v", err)
	}
	var n int64
	db.Model(&models.Position{}).Where("user_id = ? AND stock_id = ?", user.ID, stockID).Count(&n)
	if n != 0 {
		t.Errorf("positions = %d, want the position closed (AC5)", n)
	}
	expectDec(t, "balance", userRow(t, db, user.ID).Balance, "99883.38") // 100000 − 1003.50 + 886.88
	expectDec(t, "realized_pnl", realizedOf(t, db, user.ID), "-116.62")  // 886.88 − 100.35 × 10
}

// Cancel and fill of one order, concurrently, many times: exactly one commits, the other sees ORDER_NOT_OPEN, and
// cash and reservations always add up.
func TestPendingOrders_CancelVsFillRace(t *testing.T) {
	db, orders, matching, stockID := setupKall(t)
	ctx := context.Background()
	user := createUser(t, db, "100000.00")

	fills, cancels := 0, 0
	for i := range 25 {
		placed, _, err := orders.PlacePending(ctx, buyLimit10at95(user.ID, stockID))
		if err != nil {
			t.Fatal(err)
		}
		var (
			wg                 sync.WaitGroup
			fillErr, cancelErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, fillErr = matching.FillOrder(ctx, domain.FillOrderParams{OrderID: placed.ID, FillPrice: dec("94.89"),
				Commission: dec("2.37"), Total: dec("951.27"), FilledAt: time.Now()})
		}()
		go func() {
			defer wg.Done()
			_, cancelErr = orders.Cancel(ctx, user.ID, placed.ID)
		}()
		wg.Wait()

		switch {
		case fillErr == nil && errors.Is(cancelErr, domain.ErrOrderNotOpen):
			fills++
		case cancelErr == nil && errors.Is(fillErr, domain.ErrOrderNotOpen):
			cancels++
		default:
			t.Fatalf("run %d: fill err %v, cancel err %v; want exactly one winner", i, fillErr, cancelErr)
		}
		expectDec(t, "reserved_cash", userRow(t, db, user.ID).ReservedCash, "0")
	}
	want := dec("100000").Sub(dec("951.27").Mul(decimal.NewFromInt(int64(fills))))
	if got := userRow(t, db, user.ID).Balance; !got.Equal(want) {
		t.Fatalf("balance = %s after %d fills, want %s", got, fills, want)
	}
	t.Logf("%d fills, %d cancels", fills, cancels)
}

func TestAdvisoryLock_OneHolderAtATime(t *testing.T) {
	db := openSchemaDB(t)
	ctx := context.Background()
	key := rand.Int64N(1<<40) + 1 // advisory locks are database-wide: a random key keeps parallel runs apart

	a, err := repository.NewAdvisoryLock(db, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := repository.NewAdvisoryLock(db, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Release(); _ = b.Release() })

	if ok, err := a.TryAcquire(ctx); err != nil || !ok {
		t.Fatalf("a.TryAcquire() = %v, %v; want the free lock", ok, err)
	}
	if ok, err := a.TryAcquire(ctx); err != nil || !ok {
		t.Fatalf("a.TryAcquire() again = %v, %v; the holder keeps it", ok, err)
	}
	if ok, err := b.TryAcquire(ctx); err != nil || ok {
		t.Fatalf("b.TryAcquire() = %v, %v; want false while a holds it", ok, err)
	}
	if err := a.Release(); err != nil {
		t.Fatalf("a.Release() error = %v", err)
	}
	if ok, err := b.TryAcquire(ctx); err != nil || !ok {
		t.Fatalf("b.TryAcquire() after release = %v, %v; want the lock", ok, err)
	}
	if ok, err := a.TryAcquire(ctx); err != nil || ok {
		t.Fatalf("a.TryAcquire() after b took over = %v, %v; want false", ok, err)
	}
}
