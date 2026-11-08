package services

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
)

// VaultService manages user vault accounts, lazy interest accrual, transfers, and projections.
type VaultService struct {
	repo     domain.VaultRepository
	userRepo domain.UserRepository
	apy      float64
	now      func() time.Time
}

func NewVaultService(
	repo domain.VaultRepository,
	userRepo domain.UserRepository,
	apy float64,
	now func() time.Time,
) *VaultService {
	if apy <= 0 {
		apy = domain.DefaultVaultAPY
	}
	if now == nil {
		now = time.Now
	}
	return &VaultService{
		repo:     repo,
		userRepo: userRepo,
		apy:      apy,
		now:      now,
	}
}

// GetVault returns the user's vault status with lazy interest accrued up to now.
func (s *VaultService) GetVault(ctx context.Context, userID uint) (*domain.Vault, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	account, err := s.repo.Accrue(ctx, userID, s.apy, s.now().UTC())
	if err != nil {
		return nil, err
	}

	var projection decimal.Decimal
	if user.Balance.IsPositive() {
		// projection on uninvested cash: cash × APY × 30 / 365
		projection = user.Balance.Mul(decimal.NewFromFloat(s.apy)).
			Mul(decimal.NewFromInt(30)).
			Div(decimal.NewFromInt(365)).
			Round(2)
	}

	return &domain.Vault{
		Balance:                            domain.MoneyFloat(account.Balance),
		APY:                                s.apy,
		InterestEarnedTotal:                domain.MoneyFloat(account.InterestEarnedTotal),
		ProjectedMonthlyInterestOnIdleCash: projection.InexactFloat64(),
	}, nil
}

// Deposit transfers cash from buying power into the vault.
func (s *VaultService) Deposit(ctx context.Context, userID uint, amount decimal.Decimal) (*domain.VaultTransaction, error) {
	if err := validateAmount(amount); err != nil {
		return nil, err
	}

	_, tx, err := s.repo.AccrueAndDeposit(ctx, userID, amount, s.apy, s.now().UTC())
	if err != nil {
		return nil, err
	}

	return &domain.VaultTransaction{
		ID:        tx.ID,
		Type:      tx.Type,
		Amount:    domain.MoneyFloat(tx.Amount),
		CreatedAt: tx.CreatedAt.UTC(),
	}, nil
}

// Withdraw transfers cash from the vault back to cash balance.
func (s *VaultService) Withdraw(ctx context.Context, userID uint, amount decimal.Decimal) (*domain.VaultTransaction, error) {
	if err := validateAmount(amount); err != nil {
		return nil, err
	}

	_, tx, err := s.repo.AccrueAndWithdraw(ctx, userID, amount, s.apy, s.now().UTC())
	if err != nil {
		return nil, err
	}

	return &domain.VaultTransaction{
		ID:        tx.ID,
		Type:      tx.Type,
		Amount:    domain.MoneyFloat(tx.Amount),
		CreatedAt: tx.CreatedAt.UTC(),
	}, nil
}

// ListTransactions returns the user's vault transactions newest first.
func (s *VaultService) ListTransactions(ctx context.Context, userID uint) (*domain.VaultTransactionListResponse, error) {
	txs, err := s.repo.ListTransactions(ctx, userID)
	if err != nil {
		return nil, err
	}

	items := make([]domain.VaultTransaction, 0, len(txs))
	for _, tx := range txs {
		items = append(items, domain.VaultTransaction{
			ID:        tx.ID,
			Type:      tx.Type,
			Amount:    domain.MoneyFloat(tx.Amount),
			CreatedAt: tx.CreatedAt.UTC(),
		})
	}

	return &domain.VaultTransactionListResponse{
		Items:      items,
		NextCursor: nil,
	}, nil
}

func validateAmount(amount decimal.Decimal) error {
	if !amount.IsPositive() {
		return &domain.ValidationError{Msg: "amount must be greater than 0"}
	}
	if !amount.Equal(amount.Truncate(moneyPlaces)) {
		return &domain.ValidationError{Msg: "amount must have at most 2 decimals"}
	}
	return nil
}
