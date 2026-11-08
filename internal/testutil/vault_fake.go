package testutil

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
	"backend/internal/models"
)

var _ domain.VaultRepository = (*VaultRepo)(nil)

// VaultRepo is an in-memory fake implementation of domain.VaultRepository.
type VaultRepo struct {
	mu           sync.Mutex
	UserRepo     *UserRepo
	Accounts     map[uint]*models.VaultAccount
	Transactions []models.VaultTransaction
	nextTxID     uint

	GetOrCreateFn       func(ctx context.Context, userID uint, now time.Time) (*models.VaultAccount, error)
	AccrueAndDepositFn  func(ctx context.Context, userID uint, amount decimal.Decimal, apy float64, now time.Time) (*models.VaultAccount, *models.VaultTransaction, error)
	AccrueAndWithdrawFn func(ctx context.Context, userID uint, amount decimal.Decimal, apy float64, now time.Time) (*models.VaultAccount, *models.VaultTransaction, error)
	AccrueFn            func(ctx context.Context, userID uint, apy float64, now time.Time) (*models.VaultAccount, error)
	ListTransactionsFn  func(ctx context.Context, userID uint) ([]models.VaultTransaction, error)
	GetVaultBalanceFn   func(ctx context.Context, userID uint, apy float64, now time.Time) (decimal.Decimal, error)
}

func NewVaultRepo(userRepo *UserRepo) *VaultRepo {
	return &VaultRepo{
		UserRepo: userRepo,
		Accounts: make(map[uint]*models.VaultAccount),
	}
}

func (r *VaultRepo) GetOrCreate(ctx context.Context, userID uint, now time.Time) (*models.VaultAccount, error) {
	if r.GetOrCreateFn != nil {
		return r.GetOrCreateFn(ctx, userID, now)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.Accounts[userID]
	if !ok {
		acct = &models.VaultAccount{
			ID:                  uint(len(r.Accounts) + 1),
			UserID:              userID,
			Balance:             decimal.Zero,
			InterestEarnedTotal: decimal.Zero,
			LastAccruedAt:       now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		r.Accounts[userID] = acct
	}
	copy := *acct
	return &copy, nil
}

func (r *VaultRepo) AccrueAndDeposit(
	ctx context.Context,
	userID uint,
	amount decimal.Decimal,
	apy float64,
	now time.Time,
) (*models.VaultAccount, *models.VaultTransaction, error) {
	if r.AccrueAndDepositFn != nil {
		return r.AccrueAndDepositFn(ctx, userID, amount, apy, now)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var userBalance *decimal.Decimal
	if r.UserRepo != nil {
		user, err := r.UserRepo.GetByID(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		userBalance = &user.Balance
	}

	acct, ok := r.Accounts[userID]
	if !ok {
		acct = &models.VaultAccount{
			ID:                  uint(len(r.Accounts) + 1),
			UserID:              userID,
			Balance:             decimal.Zero,
			InterestEarnedTotal: decimal.Zero,
			LastAccruedAt:       now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		r.Accounts[userID] = acct
	}

	newBalance, accrued := domain.AccrueInterest(acct.Balance, acct.LastAccruedAt, now, apy)
	acct.Balance = newBalance
	acct.InterestEarnedTotal = acct.InterestEarnedTotal.Add(accrued)
	acct.LastAccruedAt = now

	if userBalance != nil {
		if userBalance.LessThan(amount) {
			return nil, nil, domain.ErrInsufficientFunds
		}
		*userBalance = userBalance.Sub(amount)
	}

	acct.Balance = acct.Balance.Add(amount)
	r.nextTxID++
	tx := models.VaultTransaction{
		ID:        r.nextTxID,
		UserID:    userID,
		Type:      domain.VaultTransactionDeposit,
		Amount:    amount,
		CreatedAt: now,
	}
	r.Transactions = append(r.Transactions, tx)

	acctCopy := *acct
	txCopy := tx
	return &acctCopy, &txCopy, nil
}

func (r *VaultRepo) AccrueAndWithdraw(
	ctx context.Context,
	userID uint,
	amount decimal.Decimal,
	apy float64,
	now time.Time,
) (*models.VaultAccount, *models.VaultTransaction, error) {
	if r.AccrueAndWithdrawFn != nil {
		return r.AccrueAndWithdrawFn(ctx, userID, amount, apy, now)
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var userBalance *decimal.Decimal
	if r.UserRepo != nil {
		user, err := r.UserRepo.GetByID(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		userBalance = &user.Balance
	}

	acct, ok := r.Accounts[userID]
	if !ok {
		acct = &models.VaultAccount{
			ID:                  uint(len(r.Accounts) + 1),
			UserID:              userID,
			Balance:             decimal.Zero,
			InterestEarnedTotal: decimal.Zero,
			LastAccruedAt:       now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		r.Accounts[userID] = acct
	}

	newBalance, accrued := domain.AccrueInterest(acct.Balance, acct.LastAccruedAt, now, apy)
	acct.Balance = newBalance
	acct.InterestEarnedTotal = acct.InterestEarnedTotal.Add(accrued)
	acct.LastAccruedAt = now

	if acct.Balance.LessThan(amount) {
		return nil, nil, domain.ErrInsufficientVaultBalance
	}

	acct.Balance = acct.Balance.Sub(amount)
	if userBalance != nil {
		*userBalance = userBalance.Add(amount)
	}

	r.nextTxID++
	tx := models.VaultTransaction{
		ID:        r.nextTxID,
		UserID:    userID,
		Type:      domain.VaultTransactionWithdrawal,
		Amount:    amount,
		CreatedAt: now,
	}
	r.Transactions = append(r.Transactions, tx)

	acctCopy := *acct
	txCopy := tx
	return &acctCopy, &txCopy, nil
}

func (r *VaultRepo) Accrue(ctx context.Context, userID uint, apy float64, now time.Time) (*models.VaultAccount, error) {
	if r.AccrueFn != nil {
		return r.AccrueFn(ctx, userID, apy, now)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.Accounts[userID]
	if !ok {
		acct = &models.VaultAccount{
			ID:                  uint(len(r.Accounts) + 1),
			UserID:              userID,
			Balance:             decimal.Zero,
			InterestEarnedTotal: decimal.Zero,
			LastAccruedAt:       now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		r.Accounts[userID] = acct
	}
	newBalance, accrued := domain.AccrueInterest(acct.Balance, acct.LastAccruedAt, now, apy)
	acct.Balance = newBalance
	acct.InterestEarnedTotal = acct.InterestEarnedTotal.Add(accrued)
	acct.LastAccruedAt = now
	copy := *acct
	return &copy, nil
}

func (r *VaultRepo) ListTransactions(ctx context.Context, userID uint) ([]models.VaultTransaction, error) {
	if r.ListTransactionsFn != nil {
		return r.ListTransactionsFn(ctx, userID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []models.VaultTransaction
	for _, tx := range r.Transactions {
		if tx.UserID == userID {
			out = append(out, tx)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *VaultRepo) GetVaultBalance(ctx context.Context, userID uint, apy float64, now time.Time) (decimal.Decimal, error) {
	if r.GetVaultBalanceFn != nil {
		return r.GetVaultBalanceFn(ctx, userID, apy, now)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	acct, ok := r.Accounts[userID]
	if !ok {
		return decimal.Zero, nil
	}
	newBalance, _ := domain.AccrueInterest(acct.Balance, acct.LastAccruedAt, now, apy)
	return newBalance.Round(2), nil
}
