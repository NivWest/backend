package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"backend/internal/domain"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
)

func TestVaultHandler(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	setup := func(initialCash float64) (*testutil.AuthRepo, *testutil.UserRepo, *testutil.VaultRepo, *VaultHandler) {
		users := testutil.NewUserRepo()
		user := &models.User{
			ID:      1,
			Email:   "member@example.com",
			Role:    "member",
			Balance: decimal.NewFromFloat(initialCash),
		}
		users.Users[1] = user

		auth := testutil.NewAuthRepo().WithSession(memberSession, user)
		vaultRepo := testutil.NewVaultRepo(users)
		vaultService := services.NewVaultService(vaultRepo, users, 0.051, func() time.Time { return now })
		handler := NewVaultHandler(vaultService, auth)
		return auth, users, vaultRepo, handler
	}

	t.Run("GET /api/v1/vault requires auth", func(t *testing.T) {
		_, _, _, handler := setup(100000)
		engine := newEngine(handler)

		res := do(t, engine, "GET", "/api/v1/vault", "", nil)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d: %s", res.Code, res.Body.String())
		}
	})

	t.Run("GET /api/v1/vault fresh user returns 200 and AC1 values", func(t *testing.T) {
		_, _, _, handler := setup(100000)
		engine := newEngine(handler)

		res := do(t, engine, "GET", "/api/v1/vault", memberSession, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
		}

		var v domain.Vault
		if err := json.Unmarshal(res.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode vault: %v", err)
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
		if math.Abs(v.ProjectedMonthlyInterestOnIdleCash-419.18) > 0.01 {
			t.Errorf("got projection %f, want 419.18", v.ProjectedMonthlyInterestOnIdleCash)
		}
	})

	t.Run("POST /api/v1/vault/deposits and withdrawals success flow", func(t *testing.T) {
		_, users, _, handler := setup(100000)
		user := users.Users[1]
		engine := newEngine(handler)

		// Deposit 20000 (AC2)
		depRes := do(t, engine, "POST", "/api/v1/vault/deposits", memberSession, `{"amount": 20000}`)
		if depRes.Code != http.StatusCreated {
			t.Fatalf("deposit expected 201, got %d: %s", depRes.Code, depRes.Body.String())
		}

		var depTx domain.VaultTransaction
		if err := json.Unmarshal(depRes.Body.Bytes(), &depTx); err != nil {
			t.Fatalf("decode deposit tx: %v", err)
		}
		if depTx.Type != "DEPOSIT" || depTx.Amount != 20000 {
			t.Errorf("unexpected depTx: %+v", depTx)
		}
		if !user.Balance.Equal(decimal.NewFromInt(80000)) {
			t.Errorf("expected user balance 80,000, got %s", user.Balance)
		}

		// GET vault after deposit
		getRes := do(t, engine, "GET", "/api/v1/vault", memberSession, nil)
		var v domain.Vault
		_ = json.Unmarshal(getRes.Body.Bytes(), &v)
		if math.Abs(v.Balance-20000) > 0.01 {
			t.Errorf("expected vault balance 20,000, got %f", v.Balance)
		}

		// Withdraw 5000 (AC3)
		wRes := do(t, engine, "POST", "/api/v1/vault/withdrawals", memberSession, `{"amount": 5000}`)
		if wRes.Code != http.StatusCreated {
			t.Fatalf("withdraw expected 201, got %d: %s", wRes.Code, wRes.Body.String())
		}

		var wTx domain.VaultTransaction
		_ = json.Unmarshal(wRes.Body.Bytes(), &wTx)
		if wTx.Type != "WITHDRAWAL" || wTx.Amount != 5000 {
			t.Errorf("unexpected wTx: %+v", wTx)
		}
		if !user.Balance.Equal(decimal.NewFromInt(85000)) {
			t.Errorf("expected user balance 85,000, got %s", user.Balance)
		}

		// GET transactions (AC5)
		txRes := do(t, engine, "GET", "/api/v1/vault/transactions", memberSession, nil)
		if txRes.Code != http.StatusOK {
			t.Fatalf("list transactions expected 200, got %d", txRes.Code)
		}
		var txList domain.VaultTransactionListResponse
		_ = json.Unmarshal(txRes.Body.Bytes(), &txList)
		if len(txList.Items) != 2 {
			t.Fatalf("expected 2 transactions, got %d", len(txList.Items))
		}
		if txList.Items[0].Type != "WITHDRAWAL" || txList.Items[1].Type != "DEPOSIT" {
			t.Errorf("unexpected transactions order: %+v", txList.Items)
		}
	})

	t.Run("AC4 error cases", func(t *testing.T) {
		_, _, _, handler := setup(5000)
		engine := newEngine(handler)

		// Deposit more than buying power -> 422 INSUFFICIENT_FUNDS
		res1 := do(t, engine, "POST", "/api/v1/vault/deposits", memberSession, `{"amount": 10000}`)
		if res1.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422, got %d: %s", res1.Code, res1.Body.String())
		}
		var err1 domain.ErrorResponse
		_ = json.Unmarshal(res1.Body.Bytes(), &err1)
		if err1.Code != "INSUFFICIENT_FUNDS" {
			t.Errorf("expected INSUFFICIENT_FUNDS, got %s", err1.Code)
		}

		// Withdraw more than vault balance -> 422 INSUFFICIENT_VAULT_BALANCE
		res2 := do(t, engine, "POST", "/api/v1/vault/withdrawals", memberSession, `{"amount": 1000}`)
		if res2.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422, got %d: %s", res2.Code, res2.Body.String())
		}
		var err2 domain.ErrorResponse
		_ = json.Unmarshal(res2.Body.Bytes(), &err2)
		if err2.Code != "INSUFFICIENT_VAULT_BALANCE" {
			t.Errorf("expected INSUFFICIENT_VAULT_BALANCE, got %s", err2.Code)
		}

		// Amount <= 0 -> 400 VALIDATION_ERROR
		res3 := do(t, engine, "POST", "/api/v1/vault/deposits", memberSession, `{"amount": 0}`)
		if res3.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", res3.Code, res3.Body.String())
		}
		var err3 domain.ErrorResponse
		_ = json.Unmarshal(res3.Body.Bytes(), &err3)
		if err3.Code != "VALIDATION_ERROR" {
			t.Errorf("expected VALIDATION_ERROR, got %s", err3.Code)
		}

		// Amount with >2 decimals -> 400 VALIDATION_ERROR
		res4 := do(t, engine, "POST", "/api/v1/vault/deposits", memberSession, `{"amount": 10.001}`)
		if res4.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", res4.Code, res4.Body.String())
		}
		var err4 domain.ErrorResponse
		_ = json.Unmarshal(res4.Body.Bytes(), &err4)
		if err4.Code != "VALIDATION_ERROR" {
			t.Errorf("expected VALIDATION_ERROR, got %s", err4.Code)
		}
	})
}
