package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"backend/internal/domain"
	"backend/internal/models"
)

type vaultRepo struct {
	db *gorm.DB
}

func NewVaultRepository(db *gorm.DB) domain.VaultRepository {
	return &vaultRepo{db: db}
}

// GetOrCreate returns the user's vault account or creates one with zero balance if none exists.
func (r *vaultRepo) GetOrCreate(ctx context.Context, userID uint, now time.Time) (*models.VaultAccount, error) {
	var account models.VaultAccount
	db := r.db.WithContext(ctx)
	if err := db.Where("user_id = ?", userID).Take(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			account = models.VaultAccount{
				UserID:              userID,
				Balance:             decimal.Zero,
				InterestEarnedTotal: decimal.Zero,
				LastAccruedAt:       now,
			}
			if err := db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}},
				DoNothing: true,
			}).Create(&account).Error; err != nil {
				return nil, fmt.Errorf("create vault account: %w", err)
			}
			if err := db.Where("user_id = ?", userID).Take(&account).Error; err != nil {
				return nil, fmt.Errorf("get vault account after creation: %w", err)
			}
		} else {
			return nil, fmt.Errorf("get vault account: %w", err)
		}
	}
	return &account, nil
}

// AccrueAndDeposit executes lazy accrual and deposits money from buying power into the vault.
// Uses a GORM transaction and FOR UPDATE locks on User and VaultAccount in strict order.
func (r *vaultRepo) AccrueAndDeposit(
	ctx context.Context,
	userID uint,
	amount decimal.Decimal,
	apy float64,
	now time.Time,
) (*models.VaultAccount, *models.VaultTransaction, error) {
	var (
		resAccount *models.VaultAccount
		resTx      *models.VaultTransaction
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Lock user row
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrUserNotFound
			}
			return fmt.Errorf("lock user: %w", err)
		}

		// 2. Lock or create vault account
		var account models.VaultAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				account = models.VaultAccount{
					UserID:              userID,
					Balance:             decimal.Zero,
					InterestEarnedTotal: decimal.Zero,
					LastAccruedAt:       now,
				}
				if err := tx.Create(&account).Error; err != nil {
					return fmt.Errorf("create vault account: %w", err)
				}
			} else {
				return fmt.Errorf("lock vault account: %w", err)
			}
		}

		// 3. Accrue interest
		newBalance, accrued := domain.AccrueInterest(account.Balance, account.LastAccruedAt, now, apy)
		account.Balance = newBalance
		account.InterestEarnedTotal = account.InterestEarnedTotal.Add(accrued)
		account.LastAccruedAt = now

		// 4. Verify buying power
		if user.Balance.LessThan(amount) {
			return domain.ErrInsufficientFunds
		}

		// 5. Apply deposit
		user.Balance = user.Balance.Sub(amount)
		account.Balance = account.Balance.Add(amount)

		if err := tx.Model(&models.User{}).Where("id = ?", userID).Update("balance", user.Balance).Error; err != nil {
			return fmt.Errorf("update user balance: %w", err)
		}
		if err := tx.Model(&models.VaultAccount{}).Where("id = ?", account.ID).Updates(map[string]any{
			"balance":               account.Balance,
			"interest_earned_total": account.InterestEarnedTotal,
			"last_accrued_at":       account.LastAccruedAt,
		}).Error; err != nil {
			return fmt.Errorf("update vault account: %w", err)
		}

		txRow := models.VaultTransaction{
			UserID:    userID,
			Type:      domain.VaultTransactionDeposit,
			Amount:    amount,
			CreatedAt: now,
		}
		if err := tx.Create(&txRow).Error; err != nil {
			return fmt.Errorf("create vault transaction: %w", err)
		}

		resAccount = &account
		resTx = &txRow
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return resAccount, resTx, nil
}

// AccrueAndWithdraw executes lazy accrual and withdraws money from the vault back to cash.
// Uses a GORM transaction and FOR UPDATE locks on User and VaultAccount in strict order.
func (r *vaultRepo) AccrueAndWithdraw(
	ctx context.Context,
	userID uint,
	amount decimal.Decimal,
	apy float64,
	now time.Time,
) (*models.VaultAccount, *models.VaultTransaction, error) {
	var (
		resAccount *models.VaultAccount
		resTx      *models.VaultTransaction
	)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Lock user row
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&user, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrUserNotFound
			}
			return fmt.Errorf("lock user: %w", err)
		}

		// 2. Lock or create vault account
		var account models.VaultAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				account = models.VaultAccount{
					UserID:              userID,
					Balance:             decimal.Zero,
					InterestEarnedTotal: decimal.Zero,
					LastAccruedAt:       now,
				}
				if err := tx.Create(&account).Error; err != nil {
					return fmt.Errorf("create vault account: %w", err)
				}
			} else {
				return fmt.Errorf("lock vault account: %w", err)
			}
		}

		// 3. Accrue interest
		newBalance, accrued := domain.AccrueInterest(account.Balance, account.LastAccruedAt, now, apy)
		account.Balance = newBalance
		account.InterestEarnedTotal = account.InterestEarnedTotal.Add(accrued)
		account.LastAccruedAt = now

		// 4. Verify vault balance
		if account.Balance.LessThan(amount) {
			return domain.ErrInsufficientVaultBalance
		}

		// 5. Apply withdrawal
		account.Balance = account.Balance.Sub(amount)
		user.Balance = user.Balance.Add(amount)

		if err := tx.Model(&models.User{}).Where("id = ?", userID).Update("balance", user.Balance).Error; err != nil {
			return fmt.Errorf("update user balance: %w", err)
		}
		if err := tx.Model(&models.VaultAccount{}).Where("id = ?", account.ID).Updates(map[string]any{
			"balance":               account.Balance,
			"interest_earned_total": account.InterestEarnedTotal,
			"last_accrued_at":       account.LastAccruedAt,
		}).Error; err != nil {
			return fmt.Errorf("update vault account: %w", err)
		}

		txRow := models.VaultTransaction{
			UserID:    userID,
			Type:      domain.VaultTransactionWithdrawal,
			Amount:    amount,
			CreatedAt: now,
		}
		if err := tx.Create(&txRow).Error; err != nil {
			return fmt.Errorf("create vault transaction: %w", err)
		}

		resAccount = &account
		resTx = &txRow
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return resAccount, resTx, nil
}

// Accrue updates the vault balance with accrued interest up to now in a transaction with FOR UPDATE lock.
func (r *vaultRepo) Accrue(ctx context.Context, userID uint, apy float64, now time.Time) (*models.VaultAccount, error) {
	var resAccount *models.VaultAccount
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account models.VaultAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				account = models.VaultAccount{
					UserID:              userID,
					Balance:             decimal.Zero,
					InterestEarnedTotal: decimal.Zero,
					LastAccruedAt:       now,
				}
				if err := tx.Create(&account).Error; err != nil {
					return fmt.Errorf("create vault account: %w", err)
				}
				resAccount = &account
				return nil
			}
			return fmt.Errorf("lock vault account: %w", err)
		}

		newBalance, accrued := domain.AccrueInterest(account.Balance, account.LastAccruedAt, now, apy)
		account.Balance = newBalance
		account.InterestEarnedTotal = account.InterestEarnedTotal.Add(accrued)
		account.LastAccruedAt = now

		if err := tx.Model(&models.VaultAccount{}).Where("id = ?", account.ID).Updates(map[string]any{
			"balance":               account.Balance,
			"interest_earned_total": account.InterestEarnedTotal,
			"last_accrued_at":       account.LastAccruedAt,
		}).Error; err != nil {
			return fmt.Errorf("update vault account: %w", err)
		}

		resAccount = &account
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resAccount, nil
}

// ListTransactions lists transactions for a user, newest first.
func (r *vaultRepo) ListTransactions(ctx context.Context, userID uint) ([]models.VaultTransaction, error) {
	var items []models.VaultTransaction
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at desc, id desc").
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list vault transactions: %w", err)
	}
	return items, nil
}

// GetVaultBalance returns current vault balance (accrued to now) without modifying database.
func (r *vaultRepo) GetVaultBalance(ctx context.Context, userID uint, apy float64, now time.Time) (decimal.Decimal, error) {
	var account models.VaultAccount
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).Take(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return decimal.Zero, nil
		}
		return decimal.Zero, fmt.Errorf("get vault balance: %w", err)
	}
	newBalance, _ := domain.AccrueInterest(account.Balance, account.LastAccruedAt, now, apy)
	return newBalance.Round(2), nil
}
