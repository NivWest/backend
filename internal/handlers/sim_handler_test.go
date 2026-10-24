package handlers

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"backend/internal/domain"
	"backend/internal/testutil"
)

const simPricePath = "/api/v1/sim/instruments/SIM-FJRD/price"

func TestSimHandler_SetPrice(t *testing.T) {
	t.Run("pins the price and returns 204", func(t *testing.T) {
		var gotID string
		var gotPrice float64
		control := &testutil.SimControl{SetPriceFn: func(_ context.Context, id string, price float64) error {
			gotID, gotPrice = id, price
			return nil
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodPut, simPricePath, "", `{"price":100}`)
		expectStatus(t, w, http.StatusNoContent)
		if w.Body.Len() != 0 {
			t.Errorf("204 must have no body, got %s", w.Body.String())
		}
		if gotID != "SIM-FJRD" || gotPrice != 100 {
			t.Errorf("SetPrice(%q, %v), want (SIM-FJRD, 100)", gotID, gotPrice)
		}
	})

	invalid := map[string]any{
		"missing body":   nil,
		"empty body":     "",
		"malformed json": "{",
		"missing price":  `{}`,
		"zero price":     `{"price":0}`,
		"negative price": `{"price":-5}`,
		"string price":   `{"price":"100"}`,
	}
	for name, body := range invalid {
		t.Run("rejects "+name, func(t *testing.T) {
			called := false
			control := &testutil.SimControl{SetPriceFn: func(context.Context, string, float64) error {
				called = true
				return nil
			}}
			w := do(t, newEngine(NewSimHandler(control)), http.MethodPut, simPricePath, "", body)
			expectMarketError(t, w, http.StatusBadRequest, domain.CodeValidation, "price must be greater than 0")
			if called {
				t.Error("SetPrice must not be called for an invalid body")
			}
		})
	}

	t.Run("unknown id is 404", func(t *testing.T) {
		control := &testutil.SimControl{SetPriceFn: func(context.Context, string, float64) error {
			return domain.ErrInstrumentNotFound
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodPut, "/api/v1/sim/instruments/SIM-NOPE/price", "", `{"price":100}`)
		expectMarketError(t, w, http.StatusNotFound, domain.CodeInstrumentNotFound, "instrument not found")
	})

	t.Run("control validation error is 400", func(t *testing.T) {
		control := &testutil.SimControl{SetPriceFn: func(context.Context, string, float64) error {
			return &domain.ValidationError{Msg: "price must be greater than 0"}
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodPut, simPricePath, "", `{"price":0.001}`)
		expectMarketError(t, w, http.StatusBadRequest, domain.CodeValidation, "price must be greater than 0")
	})

	t.Run("unexpected error is 500", func(t *testing.T) {
		control := &testutil.SimControl{SetPriceFn: func(context.Context, string, float64) error {
			return fmt.Errorf("boom")
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodPut, simPricePath, "", `{"price":100}`)
		expectMarketError(t, w, http.StatusInternalServerError, domain.CodeInternal, "")
	})
}

func TestSimHandler_ClearPrice(t *testing.T) {
	t.Run("clears the pin and returns 204", func(t *testing.T) {
		var gotID string
		control := &testutil.SimControl{ClearPriceFn: func(_ context.Context, id string) error {
			gotID = id
			return nil
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodDelete, simPricePath, "", nil)
		expectStatus(t, w, http.StatusNoContent)
		if w.Body.Len() != 0 {
			t.Errorf("204 must have no body, got %s", w.Body.String())
		}
		if gotID != "SIM-FJRD" {
			t.Errorf("ClearPrice(%q), want SIM-FJRD", gotID)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		control := &testutil.SimControl{ClearPriceFn: func(context.Context, string) error {
			return domain.ErrInstrumentNotFound
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodDelete, "/api/v1/sim/instruments/SIM-NOPE/price", "", nil)
		expectMarketError(t, w, http.StatusNotFound, domain.CodeInstrumentNotFound, "instrument not found")
	})

	t.Run("unexpected error is 500", func(t *testing.T) {
		control := &testutil.SimControl{ClearPriceFn: func(context.Context, string) error {
			return fmt.Errorf("boom")
		}}
		w := do(t, newEngine(NewSimHandler(control)), http.MethodDelete, simPricePath, "", nil)
		expectMarketError(t, w, http.StatusInternalServerError, domain.CodeInternal, "")
	})
}

func TestSimHandler_IsNotRateLimited(t *testing.T) {
	r := newEngine(NewSimHandler(&testutil.SimControl{}))
	for i := 0; i < 30; i++ {
		w := do(t, r, http.MethodDelete, simPricePath, "", nil)
		expectStatus(t, w, http.StatusNoContent)
	}
}
