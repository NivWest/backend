package domain

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/models"
)

// Vault error codes, returned as ErrorResponse.Code.
const (
	CodeInsufficientVaultBalance = "INSUFFICIENT_VAULT_BALANCE"
)

var (
	ErrInsufficientVaultBalance = errors.New("insufficient vault balance")
)

const (
	VaultTransactionDeposit    = "DEPOSIT"
	VaultTransactionWithdrawal = "WITHDRAWAL"
	VaultLabel                 = "Vault"
)

// DefaultVaultAPY is the default risk-free APY for the vault (5.1%).
const DefaultVaultAPY = 0.051

// Vault is the DTO returned by GET /api/v1/vault.
type Vault struct {
	Balance                            float64 `json:"balance"`
	APY                                float64 `json:"apy"`
	InterestEarnedTotal                float64 `json:"interest_earned_total"`
	ProjectedMonthlyInterestOnIdleCash float64 `json:"projected_monthly_interest_on_idle_cash"`
}

// VaultTransferRequest is the DTO for POST /api/v1/vault/deposits and /withdrawals.
type VaultTransferRequest struct {
	Amount float64 `json:"amount"`
}

// VaultTransaction is the DTO for vault transaction records.
type VaultTransaction struct {
	ID        uint      `json:"id"`
	Type      string    `json:"type"`
	Amount    float64   `json:"amount"`
	CreatedAt time.Time `json:"created_at"`
}

// VaultTransactionListResponse is the response for GET /api/v1/vault/transactions.
type VaultTransactionListResponse struct {
	Items      []VaultTransaction `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}

// VaultRepository manages the persistence of vault accounts and transactions.
type VaultRepository interface {
	// GetOrCreate returns the user's vault account or creates one with zero balance if none exists.
	GetOrCreate(ctx context.Context, userID uint, now time.Time) (*models.VaultAccount, error)
	// AccrueAndDeposit accrues interest and deposits amount from user balance into vault account.
	AccrueAndDeposit(ctx context.Context, userID uint, amount decimal.Decimal, apy float64, now time.Time) (*models.VaultAccount, *models.VaultTransaction, error)
	// AccrueAndWithdraw accrues interest and withdraws amount from vault account to user balance.
	AccrueAndWithdraw(ctx context.Context, userID uint, amount decimal.Decimal, apy float64, now time.Time) (*models.VaultAccount, *models.VaultTransaction, error)
	// Accrue updates the vault balance with accrued interest up to now.
	Accrue(ctx context.Context, userID uint, apy float64, now time.Time) (*models.VaultAccount, error)
	// ListTransactions lists transactions for a user, newest first.
	ListTransactions(ctx context.Context, userID uint) ([]models.VaultTransaction, error)
	// GetVaultBalance returns current vault balance (accrued to now) for valuation in portfolio / snapshots.
	GetVaultBalance(ctx context.Context, userID uint, apy float64, now time.Time) (decimal.Decimal, error)
}

// AccrueInterest computes lazy compounding interest: balance × (1 + APY)^(elapsed_days/365).
// It returns the new balance and the accrued interest earned in the elapsed duration.
func AccrueInterest(balance decimal.Decimal, lastAccruedAt, now time.Time, apy float64) (newBalance decimal.Decimal, accrued decimal.Decimal) {
	if !balance.IsPositive() || now.Before(lastAccruedAt) || now.Equal(lastAccruedAt) {
		return balance, decimal.Zero
	}
	elapsedSeconds := now.Sub(lastAccruedAt).Seconds()
	if elapsedSeconds <= 0 {
		return balance, decimal.Zero
	}
	elapsedDays := elapsedSeconds / 86400.0
	growth := math.Pow(1.0+apy, elapsedDays/365.0)
	newBalance = balance.Mul(decimal.NewFromFloat(growth))
	accrued = newBalance.Sub(balance)
	return newBalance, accrued
}
