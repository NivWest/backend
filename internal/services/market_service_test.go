package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"backend/internal/domain"
	"backend/internal/testutil"
)

func expectValidationError(t *testing.T, err error) {
	t.Helper()
	var v *domain.ValidationError
	if !errors.As(err, &v) || v.Msg == "" {
		t.Fatalf("error = %v, want *domain.ValidationError", err)
	}
}

func TestMarketService_Search_RejectsBlankQuery(t *testing.T) {
	called := false
	svc := NewMarketService(&testutil.MarketProvider{SearchFn: func(context.Context, string) ([]domain.InstrumentSummary, error) {
		called = true
		return nil, nil
	}})
	for _, q := range []string{"", "   ", "\t\n"} {
		_, err := svc.Search(context.Background(), q)
		expectValidationError(t, err)
		var v *domain.ValidationError
		if errors.As(err, &v) && v.Msg != "q must not be blank" {
			t.Errorf("Search(%q) message = %q", q, v.Msg)
		}
	}
	if called {
		t.Error("provider must not be called for a blank query")
	}
}

func TestMarketService_Search_TrimsAndNormalisesNil(t *testing.T) {
	var gotQuery string
	svc := NewMarketService(&testutil.MarketProvider{SearchFn: func(_ context.Context, q string) ([]domain.InstrumentSummary, error) {
		gotQuery = q
		return nil, nil
	}})
	items, err := svc.Search(context.Background(), "  fjord ")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if gotQuery != "fjord" {
		t.Errorf("provider query = %q, want fjord", gotQuery)
	}
	if items == nil || len(items) != 0 {
		t.Errorf("Search() = %#v, want empty non-nil slice", items)
	}
}

func TestMarketService_Search_ReturnsProviderItems(t *testing.T) {
	want := []domain.InstrumentSummary{{OrderbookID: "SIM-FJRD", Symbol: "FJRD"}}
	svc := NewMarketService(&testutil.MarketProvider{SearchFn: func(context.Context, string) ([]domain.InstrumentSummary, error) {
		return want, nil
	}})
	items, err := svc.Search(context.Background(), "fjord")
	if err != nil || len(items) != 1 || items[0].OrderbookID != "SIM-FJRD" {
		t.Fatalf("Search() = %+v, %v", items, err)
	}
}

func TestMarketService_RejectsBlankID(t *testing.T) {
	svc := NewMarketService(&testutil.MarketProvider{})
	ctx := context.Background()
	cases := map[string]func() error{
		"quote":   func() error { _, err := svc.Quote(ctx, " "); return err },
		"details": func() error { _, err := svc.Details(ctx, ""); return err },
		"candles": func() error { _, _, err := svc.Candles(ctx, "", "1m"); return err },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) { expectValidationError(t, call()) })
	}
}

func TestMarketService_Quote_And_Details_Delegate(t *testing.T) {
	var quoteID, detailsID string
	svc := NewMarketService(&testutil.MarketProvider{
		QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
			quoteID = id
			return domain.Quote{OrderbookID: id, Last: 100}, nil
		},
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			detailsID = id
			return domain.InstrumentDetails{OrderbookID: id, Description: "x"}, nil
		},
	})
	q, err := svc.Quote(context.Background(), "SIM-FJRD")
	if err != nil || q.Last != 100 || quoteID != "SIM-FJRD" {
		t.Errorf("Quote() = %+v, %v (id %q)", q, err, quoteID)
	}
	d, err := svc.Details(context.Background(), "SIM-FJRD")
	if err != nil || d.Description != "x" || detailsID != "SIM-FJRD" {
		t.Errorf("Details() = %+v, %v (id %q)", d, err, detailsID)
	}
}

func TestMarketService_Candles_RejectsInvalidPeriod(t *testing.T) {
	called := false
	svc := NewMarketService(&testutil.MarketProvider{CandlesFn: func(context.Context, string, domain.CandlePeriod) ([]domain.Candle, error) {
		called = true
		return nil, nil
	}})
	for _, period := range []string{"", "5x", "1D", " 1d"} {
		_, _, err := svc.Candles(context.Background(), "SIM-FJRD", period)
		expectValidationError(t, err)
	}
	if called {
		t.Error("provider must not be called for an invalid period")
	}
}

func TestMarketService_Candles_AcceptsEveryPeriod(t *testing.T) {
	for _, want := range domain.CandlePeriods {
		t.Run(string(want), func(t *testing.T) {
			var got domain.CandlePeriod
			svc := NewMarketService(&testutil.MarketProvider{CandlesFn: func(_ context.Context, _ string, p domain.CandlePeriod) ([]domain.Candle, error) {
				got = p
				return []domain.Candle{{O: 1, H: 2, L: 1, C: 2, V: 3}}, nil
			}})
			candles, period, err := svc.Candles(context.Background(), "SIM-FJRD", string(want))
			if err != nil {
				t.Fatalf("Candles() error = %v", err)
			}
			if period != want || got != want {
				t.Errorf("period = %q, provider got %q, want %q", period, got, want)
			}
			if len(candles) != 1 {
				t.Errorf("candles = %+v", candles)
			}
		})
	}
}

func TestMarketService_Candles_NormalisesNil(t *testing.T) {
	svc := NewMarketService(&testutil.MarketProvider{CandlesFn: func(context.Context, string, domain.CandlePeriod) ([]domain.Candle, error) {
		return nil, nil
	}})
	candles, period, err := svc.Candles(context.Background(), "SIM-FJRD", "1w")
	if err != nil || period != domain.Period1W {
		t.Fatalf("Candles() = %q, %v", period, err)
	}
	if candles == nil || len(candles) != 0 {
		t.Errorf("candles = %#v, want empty non-nil slice", candles)
	}
}

func TestMarketService_PassesProviderErrorsThrough(t *testing.T) {
	for _, providerErr := range []error{
		domain.ErrInstrumentNotFound,
		fmt.Errorf("avanza: timeout: %w", domain.ErrMarketDataUnavailable),
		errors.New("boom"),
	} {
		svc := NewMarketService(&testutil.MarketProvider{
			SearchFn: func(context.Context, string) ([]domain.InstrumentSummary, error) { return nil, providerErr },
			QuoteFn:  func(context.Context, string) (domain.Quote, error) { return domain.Quote{}, providerErr },
			DetailsFn: func(context.Context, string) (domain.InstrumentDetails, error) {
				return domain.InstrumentDetails{}, providerErr
			},
			CandlesFn: func(context.Context, string, domain.CandlePeriod) ([]domain.Candle, error) { return nil, providerErr },
		})
		ctx := context.Background()
		_, errSearch := svc.Search(ctx, "x")
		_, errQuote := svc.Quote(ctx, "SIM-X")
		_, errDetails := svc.Details(ctx, "SIM-X")
		_, _, errCandles := svc.Candles(ctx, "SIM-X", "1d")
		for name, err := range map[string]error{"search": errSearch, "quote": errQuote, "details": errDetails, "candles": errCandles} {
			if err != providerErr {
				t.Errorf("%s error = %v, want %v unchanged", name, err, providerErr)
			}
		}
	}
}
