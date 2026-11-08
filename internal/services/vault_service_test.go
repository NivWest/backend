package services_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
)

func TestAccrueInterest(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	apy := 0.051
	initialBalance := decimal.NewFromInt(10000) // 10,000 SEK

	tests := []struct {
		name         string
		elapsedDays  float64
		wantBalance  float64
		wantAccrued  float64
		maxTolerance float64
	}{
		{
			name:         "0 days",
			elapsedDays:  0,
			wantBalance:  10000.0,
			wantAccrued:  0.0,
			maxTolerance: 0.0001,
		},
		{
			name:        "1 day",
			elapsedDays: 1,
			// 10000 * (1.051)^(1/365) = 10001.36365...
			wantBalance:  10000.0 * math.Pow(1.051, 1.0/365.0),
			wantAccrued:  10000.0 * (math.Pow(1.051, 1.0/365.0) - 1.0),
			maxTolerance: 0.0001,
		},
		{
			name:        "30 days",
			elapsedDays: 30,
			// 10000 * (1.051)^(30/365) = 10041.01865...
			wantBalance:  10000.0 * math.Pow(1.051, 30.0/365.0),
			wantAccrued:  10000.0 * (math.Pow(1.051, 30.0/365.0) - 1.0),
			maxTolerance: 0.0001,
		},
		{
			name:        "365 days equals balance * 1.051 exactly",
			elapsedDays: 365,
			// 10000 * 1.051 = 10510.00
			wantBalance:  10510.0,
			wantAccrued:  510.0,
			maxTolerance: 0.0001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := start.Add(time.Duration(tt.elapsedDays * float64(24*time.Hour)))
			gotBal, gotAcc := domain.AccrueInterest(initialBalance, start, now, apy)

			if math.Abs(gotBal.InexactFloat64()-tt.wantBalance) > tt.maxTolerance {
				t.Errorf("got balance %f, want %f", gotBal.InexactFloat64(), tt.wantBalance)
			}
			if math.Abs(gotAcc.InexactFloat64()-tt.wantAccrued) > tt.maxTolerance {
				t.Errorf("got accrued %f, want %f", gotAcc.InexactFloat64(), tt.wantAccrued)
			}
		})
	}
}

func TestVaultService_GetVault(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("fresh user returns zero balance and projected monthly interest on 100,000 cash of 419.18", func(t *testing.T) {
		userRepo := testutil.NewUserRepo()
		user := &models.User{
			ID:      1,
			Balance: decimal.NewFromInt(100000),
		}
		userRepo.Users[1] = user
		vaultRepo := testutil.NewVaultRepo(userRepo)
		svc := services.NewVaultService(vaultRepo, userRepo, 0.051, func() time.Time { return now })

		v, err := svc.GetVault(ctx, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if v.Balance != 0 {
			t.Errorf("got balance %f, want 0", v.Balance)
		}
		if v.APY != 0.051 {
			t.Errorf("got apy %f, want 0.051", v.APY)
		}
		if v.InterestEarnedTotal != 0 {
			t.Errorf("got interest earned %f, want 0", v.InterestEarnedTotal)
		}
		// 100,000 × 0.051 × 30 / 365 = 419.178... rounds to 419.18
		if math.Abs(v.ProjectedMonthlyInterestOnIdleCash-419.18) > 0.01 {
			t.Errorf("got projection %f, want 419.18", v.ProjectedMonthlyInterestOnIdleCash)
		}
	})
}

func TestVaultService_DepositAndWithdraw(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	setup := func(initialCash float64) (*services.VaultService, *models.User) {
		userRepo := testutil.NewUserRepo()
		user := &models.User{
			ID:      1,
			Balance: decimal.NewFromFloat(initialCash),
		}
		userRepo.Users[1] = user
		vaultRepo := testutil.NewVaultRepo(userRepo)
		svc := services.NewVaultService(vaultRepo, userRepo, 0.051, func() time.Time { return now })
		return svc, user
	}

	t.Run("successful deposit and withdrawal", func(t *testing.T) {
		svc, user := setup(100000)

		// Deposit 20,000
		tx1, err := svc.Deposit(ctx, 1, decimal.NewFromInt(20000))
		if err != nil {
			t.Fatalf("deposit failed: %v", err)
		}
		if tx1.Amount != 20000 || tx1.Type != domain.VaultTransactionDeposit {
			t.Errorf("unexpected tx1: %+v", tx1)
		}
		if !user.Balance.Equal(decimal.NewFromInt(80000)) {
			t.Errorf("expected user balance 80,000, got %s", user.Balance)
		}

		v, err := svc.GetVault(ctx, 1)
		if err != nil {
			t.Fatalf("get vault failed: %v", err)
		}
		if math.Abs(v.Balance-20000) > 0.01 {
			t.Errorf("expected vault balance 20,000, got %f", v.Balance)
		}

		// Withdraw 5,000
		tx2, err := svc.Withdraw(ctx, 1, decimal.NewFromInt(5000))
		if err != nil {
			t.Fatalf("withdraw failed: %v", err)
		}
		if tx2.Amount != 5000 || tx2.Type != domain.VaultTransactionWithdrawal {
			t.Errorf("unexpected tx2: %+v", tx2)
		}
		if !user.Balance.Equal(decimal.NewFromInt(85000)) {
			t.Errorf("expected user balance 85,000, got %s", user.Balance)
		}

		v2, err := svc.GetVault(ctx, 1)
		if err != nil {
			t.Fatalf("get vault failed: %v", err)
		}
		if math.Abs(v2.Balance-15000) > 0.01 {
			t.Errorf("expected vault balance 15,000, got %f", v2.Balance)
		}

		// Check transactions
		list, err := svc.ListTransactions(ctx, 1)
		if err != nil {
			t.Fatalf("list transactions failed: %v", err)
		}
		if len(list.Items) != 2 {
			t.Fatalf("expected 2 transactions, got %d", len(list.Items))
		}
		// newest first: withdrawal then deposit
		if list.Items[0].Type != domain.VaultTransactionWithdrawal || list.Items[1].Type != domain.VaultTransactionDeposit {
			t.Errorf("unexpected transactions order: %+v", list.Items)
		}
	})

	t.Run("validation errors: non-positive or >2 decimals", func(t *testing.T) {
		svc, _ := setup(100000)

		var valErr *domain.ValidationError

		// 0 amount
		_, err := svc.Deposit(ctx, 1, decimal.Zero)
		if !errors.As(err, &valErr) {
			t.Errorf("expected validation error for 0 amount, got %v", err)
		}

		// negative amount
		_, err = svc.Deposit(ctx, 1, decimal.NewFromInt(-10))
		if !errors.As(err, &valErr) {
			t.Errorf("expected validation error for negative amount, got %v", err)
		}

		// 3 decimals
		_, err = svc.Deposit(ctx, 1, decimal.RequireFromString("10.001"))
		if !errors.As(err, &valErr) {
			t.Errorf("expected validation error for 3 decimals, got %v", err)
		}

		// 3 decimals on withdraw
		_, err = svc.Withdraw(ctx, 1, decimal.RequireFromString("10.001"))
		if !errors.As(err, &valErr) {
			t.Errorf("expected validation error for 3 decimals, got %v", err)
		}
	})

	t.Run("insufficient funds when depositing more than balance", func(t *testing.T) {
		svc, _ := setup(5000)
		_, err := svc.Deposit(ctx, 1, decimal.NewFromInt(6000))
		if !errors.Is(err, domain.ErrInsufficientFunds) {
			t.Errorf("expected ErrInsufficientFunds, got %v", err)
		}
	})

	t.Run("insufficient vault balance when withdrawing more than vault balance", func(t *testing.T) {
		svc, _ := setup(50000)
		_, _ = svc.Deposit(ctx, 1, decimal.NewFromInt(10000))

		_, err := svc.Withdraw(ctx, 1, decimal.NewFromInt(15000))
		if !errors.Is(err, domain.ErrInsufficientVaultBalance) {
			t.Errorf("expected ErrInsufficientVaultBalance, got %v", err)
		}
	})
}
