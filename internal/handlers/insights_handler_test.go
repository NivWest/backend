package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/llm/fake"
	"backend/internal/models"
	"backend/internal/services"
	"backend/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func newInsightsEngine(
	users *testutil.UserRepo,
	pfRepo *testutil.PortfolioRepo,
	orderRepo *testutil.OrderRepo,
	reviewRepo *testutil.InsightsReviewRepo,
	llm domain.LLM,
	budget int64,
) *gin.Engine {
	provider := &testutil.MarketProvider{
		QuoteFn: func(_ context.Context, id string) (domain.Quote, error) {
			return domain.Quote{OrderbookID: id, Symbol: "SOLV", Last: 100, Bid: 99.9, Ask: 100.1}, nil
		},
		DetailsFn: func(_ context.Context, id string) (domain.InstrumentDetails, error) {
			return domain.InstrumentDetails{OrderbookID: id, Symbol: "SOLV", Name: "Solvik Solar", LastPrice: 100}, nil
		},
	}
	pfSvc := services.NewPortfolioService(pfRepo, provider, users)
	gw := services.NewAIGateway(llm, testutil.NewAIUsageRepo(), budget, time.Second, config.DefaultModelPrices(), aiClock)
	insSvc := services.NewInsightsService(pfSvc, orderRepo, reviewRepo, gw, nil, aiClock)
	return newEngine(NewInsightsHandler(insSvc, newAuth()))
}

func TestInsightsHandler_GetInsights(t *testing.T) {
	users := testutil.NewUserRepo(&models.User{ID: 1, Balance: decimal.NewFromInt(100000)})
	engine := newInsightsEngine(users, &testutil.PortfolioRepo{}, &testutil.OrderRepo{}, testutil.NewInsightsReviewRepo(), fake.New(), 0)

	// Unauthenticated
	wUnauth := do(t, engine, http.MethodGet, "/api/v1/insights", "", nil)
	expectStatus(t, wUnauth, http.StatusUnauthorized)

	// Authenticated fresh user
	w := do(t, engine, http.MethodGet, "/api/v1/insights", memberSession, nil)
	expectStatus(t, w, http.StatusOK)
	var ins domain.Insights
	if err := json.Unmarshal(w.Body.Bytes(), &ins); err != nil {
		t.Fatal(err)
	}
	if len(ins.Detectors) != 5 {
		t.Fatalf("len(Detectors) = %d, want 5", len(ins.Detectors))
	}
	for _, d := range ins.Detectors {
		if d.Triggered {
			t.Errorf("detector %s triggered = true, want false", d.ID)
		}
	}
}

func TestInsightsHandler_GenerateAndListReviews(t *testing.T) {
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
			return []domain.Order{
				{
					ID:          42,
					OrderbookID: "SIM-SOLV",
					Symbol:      "SOLV",
					Side:        domain.OrderSideBuy,
					Quantity:    decimal.NewFromInt(500),
					FillPrice:   decimal.NewFromInt(100),
					Total:       decimal.NewFromInt(50000),
					Note:        "Solar thesis",
					CreatedAt:   aiNow,
				},
			}, nil
		},
	}
	reviewRepo := testutil.NewInsightsReviewRepo()
	engine := newInsightsEngine(users, pfRepo, orderRepo, reviewRepo, fake.New(), 0)

	// Unauthenticated POST /ai/insights/review
	expectStatus(t, do(t, engine, http.MethodPost, "/api/v1/ai/insights/review", "", nil), http.StatusUnauthorized)

	// Authenticated POST /ai/insights/review
	wPost := do(t, engine, http.MethodPost, "/api/v1/ai/insights/review", memberSession, nil)
	expectStatus(t, wPost, http.StatusCreated)
	var rev domain.InsightsReview
	if err := json.Unmarshal(wPost.Body.Bytes(), &rev); err != nil {
		t.Fatal(err)
	}
	if rev.Headline != "FAKE: weekly review" || len(rev.Observations) != 1 || rev.Observations[0].DetectorID != "concentration" {
		t.Fatalf("unexpected review: %+v", rev)
	}
	if len(rev.Observations[0].OrderIDs) != 1 || rev.Observations[0].OrderIDs[0] != 42 {
		t.Fatalf("expected only order 42 (999999999 stripped), got %v", rev.Observations[0].OrderIDs)
	}

	// GET /ai/insights/reviews
	wGet := do(t, engine, http.MethodGet, "/api/v1/ai/insights/reviews", memberSession, nil)
	expectStatus(t, wGet, http.StatusOK)
	var list domain.InsightsReviewListResponse
	if err := json.Unmarshal(wGet.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != rev.ID {
		t.Fatalf("list = %+v, want 1 item matching generated review", list)
	}
}
