package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/llm/fake"
	"backend/internal/models"
	"backend/internal/testutil"

	"github.com/shopspring/decimal"
)

func newInsightsTestService(
	users *testutil.UserRepo,
	pfRepo *testutil.PortfolioRepo,
	orderRepo *testutil.OrderRepo,
	reviewRepo *testutil.InsightsReviewRepo,
	llm domain.LLM,
) *InsightsService {
	provider := &testutil.MarketProvider{
		QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
			return domain.Quote{OrderbookID: id, Symbol: "SOLV", Last: 100, Bid: 99.9, Ask: 100.1}, nil
		},
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			return domain.InstrumentDetails{OrderbookID: id, Symbol: "SOLV", Name: "Solvik Solar", LastPrice: 100}, nil
		},
	}
	pfSvc := NewPortfolioService(pfRepo, provider, users)
	gw := NewAIGateway(llm, testutil.NewAIUsageRepo(), 0, time.Second, config.DefaultModelPrices(), fixedNow)
	return NewInsightsService(pfSvc, orderRepo, reviewRepo, gw, nil, fixedNow)
}

func TestInsightsService_GetInsights_FreshUserAllFalse(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.NewFromInt(100000)})
	pfRepo := &testutil.PortfolioRepo{}
	orderRepo := &testutil.OrderRepo{}
	reviewRepo := testutil.NewInsightsReviewRepo()

	svc := newInsightsTestService(users, pfRepo, orderRepo, reviewRepo, fake.New())
	ins, err := svc.GetInsights(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetInsights() error = %v", err)
	}
	if len(ins.Detectors) != 5 {
		t.Fatalf("len(Detectors) = %d, want 5", len(ins.Detectors))
	}
	for _, d := range ins.Detectors {
		if d.Triggered {
			t.Errorf("detector %s triggered = true on fresh user, want false", d.ID)
		}
	}
}

func TestInsightsService_GenerateReview_PostValidationDropsFakeHallucinationAndUntriggered(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.NewFromInt(50000)})
	pfRepo := &testutil.PortfolioRepo{
		Positions: []models.Position{
			{
				UserID:   1,
				StockID:  1,
				Stock:    models.Stock{ID: 1, OrderbookID: "SIM-SOLV", Symbol: "SOLV", Name: "Solvik Solar"},
				Quantity: decimal.NewFromInt(500),
				AvgPrice: decimal.NewFromInt(100),
				AvgCost:  decimal.NewFromInt(100),
			},
		},
	}
	orderRepo := &testutil.OrderRepo{
		ListAllByUserFn: func(_ context.Context, userID uint) ([]domain.Order, error) {
			if userID != 1 {
				return nil, nil
			}
			return []domain.Order{
				{
					ID:          42,
					OrderbookID: "SIM-SOLV",
					Symbol:      "SOLV",
					Side:        domain.OrderSideBuy,
					Quantity:    decimal.NewFromInt(500),
					FillPrice:   decimal.NewFromInt(100),
					Total:       decimal.NewFromInt(50000),
					Note:        "Thesis on solar",
					CreatedAt:   gatewayNow,
				},
			}, nil
		},
	}
	reviewRepo := testutil.NewInsightsReviewRepo()

	// Test with the fake provider which injects 999999999 into the first observation
	svc := newInsightsTestService(users, pfRepo, orderRepo, reviewRepo, fake.New())
	review, err := svc.GenerateReview(context.Background(), 1)
	if err != nil {
		t.Fatalf("GenerateReview() error = %v", err)
	}
	if review.ID != 1 {
		t.Errorf("review.ID = %d, want 1", review.ID)
	}
	if review.Headline != "FAKE: weekly review" {
		t.Errorf("Headline = %q, want %q", review.Headline, "FAKE: weekly review")
	}
	if review.Disclaimer != domain.AIDisclaimer {
		t.Errorf("Disclaimer = %q, want %q", review.Disclaimer, domain.AIDisclaimer)
	}
	if len(review.Observations) != 1 {
		t.Fatalf("len(Observations) = %d, want 1", len(review.Observations))
	}
	obs := review.Observations[0]
	if obs.DetectorID != "concentration" {
		t.Errorf("DetectorID = %q, want concentration", obs.DetectorID)
	}
	for _, id := range obs.OrderIDs {
		if id == fake.FakeHallucinatedOrderID {
			t.Fatalf("post-validation failed to strip hallucinated order ID %d", id)
		}
	}
	if len(obs.OrderIDs) != 1 || obs.OrderIDs[0] != 42 {
		t.Errorf("OrderIDs = %v, want [42]", obs.OrderIDs)
	}

	// Verify ListReviews returns the persisted review
	list, err := svc.ListReviews(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListReviews() error = %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != 1 {
		t.Errorf("ListReviews() = %+v, want 1 item with ID 1", list)
	}
}

func TestValidateReviewObservations_DropsUntriggeredDetectorsAndForeignOrders(t *testing.T) {
	detectors := []domain.DetectorResult{
		{ID: domain.DetectorConcentration, Triggered: true},
		{ID: domain.DetectorRevengeTrading, Triggered: false},
	}
	orders := []domain.Order{{ID: 10}, {ID: 20}}
	raw := []domain.ReviewObservation{
		{DetectorID: "concentration", Narrative: "Valid", OrderIDs: []uint{10, 999999999, 20, 10, 777}},
		{DetectorID: "revenge_trading", Narrative: "Untriggered", OrderIDs: []uint{10}},
		{DetectorID: "invented_detector", Narrative: "Bogus", OrderIDs: []uint{10}},
	}
	got := validateReviewObservations(raw, detectors, orders)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DetectorID != "concentration" {
		t.Errorf("DetectorID = %q, want concentration", got[0].DetectorID)
	}
	if len(got[0].OrderIDs) != 2 || got[0].OrderIDs[0] != 10 || got[0].OrderIDs[1] != 20 {
		t.Errorf("OrderIDs = %v, want [10, 20]", got[0].OrderIDs)
	}
}

func TestInsightsService_GenerateReview_InvalidLLMJSON(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.NewFromInt(100000)})
	badLLM := &testutil.LLM{
		GenerateFn: func(context.Context, domain.LLMRequest) (domain.LLMResponse, error) {
			return domain.LLMResponse{Content: `{"headline":""}`, Model: "bad"}, nil
		},
	}
	svc := newInsightsTestService(users, &testutil.PortfolioRepo{}, &testutil.OrderRepo{}, testutil.NewInsightsReviewRepo(), badLLM)
	_, err := svc.GenerateReview(context.Background(), 1)
	if !errors.Is(err, domain.ErrAIUnavailable) {
		t.Fatalf("error = %v, want ErrAIUnavailable", err)
	}
}
