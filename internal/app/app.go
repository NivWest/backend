package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"backend/api"
	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/handlers"
	"backend/internal/integrations/anthropic"
	"backend/internal/integrations/avanza"
	"backend/internal/integrations/database"
	"backend/internal/integrations/simmarket"
	"backend/internal/llm/fake"
	"backend/internal/repository"
	"backend/internal/services"
)

type App struct {
	HealthHandler    *handlers.HealthHandler
	OpenAPIHandler   *handlers.OpenAPIHandler
	UserHandler      *handlers.UserHandler
	MarketHandler    *handlers.MarketHandler
	AuthHandler      *handlers.AuthHandler
	PortfolioHandler *handlers.PortfolioHandler
	OrderHandler     *handlers.OrderHandler
	WatchlistHandler *handlers.WatchlistHandler
	AIHandler        *handlers.AIHandler
	AnalyticsHandler *handlers.AnalyticsHandler
	InsightsHandler  *handlers.InsightsHandler
	AuthRepo         domain.AuthRepository

	// SimHandler is nil unless MARKET_DATA_PROVIDER=simulated and MARKET_SIM_CONTROL_ENABLED=true (dev/test only).
	SimHandler *handlers.SimHandler
}

func NewApp(cfg *config.Config) (*App, func() error, error) {
	db, err := database.NewPostgresDB(cfg)
	if err != nil {
		return nil, nil, err
	}

	if err := database.Migrate(db); err != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, nil, fmt.Errorf("get sql database: %w", err)
		}
		_ = sqlDB.Close()
		return nil, nil, err
	}

	userRepo := repository.NewUserRepository(db)
	authRepo := repository.NewAuthRepository(db)
	userService := services.NewUserService(userRepo)
	authService := services.NewAuthService(userRepo, authRepo, cfg)
	userHandler := handlers.NewUserHandler(userService, authRepo)
	authHandler := handlers.NewAuthHandler(authService, cfg)

	httpClient := &http.Client{
		Timeout: cfg.Avanza.Timeout,
	}

	// The market data port: one provider instance, selected by MARKET_DATA_PROVIDER, shared by
	// market data, portfolios and orders (so a pinned simulated price is what orders fill at).
	warmCtx, cancelWarm := context.WithCancel(context.Background())
	provider, simHandler := newMarketProvider(warmCtx, cfg, httpClient)
	marketHandler := handlers.NewMarketHandler(services.NewMarketService(provider))

	portfolioRepo := repository.NewPortfolioRepository(db)
	portfolioService := services.NewPortfolioService(portfolioRepo, provider, userRepo)
	portfolioHandler := handlers.NewPortfolioHandler(portfolioService, authRepo)

	// Equity snapshots (story NW-10): one after every fill, and one per user with an order every SNAPSHOT_INTERVAL.
	snapshotRepo := repository.NewSnapshotRepository(db)
	snapshotService := services.NewSnapshotService(snapshotRepo, portfolioRepo, userRepo, provider, time.Now)
	analyticsService := services.NewAnalyticsService(snapshotRepo, portfolioService, cfg.Analytics.RiskFreeRate, time.Now)
	analyticsHandler := handlers.NewAnalyticsHandler(analyticsService, authRepo)

	orderRepo := repository.NewOrderRepository(db)
	orderService := services.NewOrderService(orderRepo, provider)
	orderService.SetFillObserver(snapshotService)
	orderHandler := handlers.NewOrderHandler(orderService, authRepo)

	watchlistRepo := repository.NewWatchlistRepository(db)
	watchlistService := services.NewWatchlistService(watchlistRepo, provider)
	watchlistHandler := handlers.NewWatchlistHandler(watchlistService, authRepo)

	// The LLM gateway (story NW-14): one provider, selected by LLM_PROVIDER, behind budget + usage ledger.
	llm := newLLM(cfg)
	gateway := services.NewAIGateway(llm, repository.NewAIUsageRepository(db), cfg.LLM.DailyTokenBudget, cfg.LLM.Timeout, cfg.LLM.Prices, time.Now)
	briefService := services.NewAIBriefService(gateway, provider, repository.NewAIBriefRepository(db), time.Now)
	aiHandler := handlers.NewAIHandler(briefService, gateway, authRepo)

	insightsReviewRepo := repository.NewInsightsReviewRepository(db)
	insightsService := services.NewInsightsService(portfolioService, orderRepo, insightsReviewRepo, gateway, nil, time.Now)
	insightsHandler := handlers.NewInsightsHandler(insightsService, authRepo)

	// The guest purger runs until closeApp cancels its context: once now, then every GuestPurgeInterval.
	guestRepo := repository.NewGuestRepository(db)
	purger := services.NewGuestPurger(guestRepo, domain.GuestRetention, time.Now)
	purgeCtx, cancelPurge := context.WithCancel(context.Background())
	go purger.Run(purgeCtx, domain.GuestPurgeInterval)

	snapshotCtx, cancelSnapshots := context.WithCancel(context.Background())
	go runSnapshotScheduler(snapshotCtx, newTimeTicker(cfg.Analytics.SnapshotInterval), snapshotService)
	log.Printf("equity snapshots every %s, risk-free rate %.4f", cfg.Analytics.SnapshotInterval, cfg.Analytics.RiskFreeRate)

	closeApp := func() error {
		cancelSnapshots()
		cancelPurge()
		cancelWarm()
		sqlDB, err := db.DB()
		if err != nil {
			return fmt.Errorf("get sql database: %w", err)
		}
		return sqlDB.Close()
	}

	return &App{
		HealthHandler: handlers.NewHealthHandler(
			domain.Features{GuestAuth: cfg.Auth.GuestEnabled, SimControl: simHandler != nil},
			cfg.Market.Provider,
			llm.Name(),
		),
		OpenAPIHandler:   handlers.NewOpenAPIHandler(api.OpenAPISpec),
		UserHandler:      userHandler,
		MarketHandler:    marketHandler,
		SimHandler:       simHandler,
		AuthHandler:      authHandler,
		PortfolioHandler: portfolioHandler,
		OrderHandler:     orderHandler,
		WatchlistHandler: watchlistHandler,
		AIHandler:        aiHandler,
		AnalyticsHandler: analyticsHandler,
		InsightsHandler:  insightsHandler,
		AuthRepo:         authRepo,
	}, closeApp, nil
}

// newMarketProvider builds the market data provider selected by cfg.Market.Provider and, when the
// simulated provider runs with MARKET_SIM_CONTROL_ENABLED=true, the dev/test price-pinning handler
// (nil otherwise). The simulated market is warmed in the background until ctx is cancelled.
func newMarketProvider(
	ctx context.Context,
	cfg *config.Config,
	httpClient *http.Client,
) (domain.MarketDataProvider, *handlers.SimHandler) {
	if cfg.Market.Provider != config.MarketProviderSimulated {
		log.Printf("market data provider: %s (%s)", config.MarketProviderAvanza, cfg.Avanza.BaseURL)
		if cfg.Market.SimControlEnabled {
			log.Printf("WARNING: MARKET_SIM_CONTROL_ENABLED=true is ignored: sim control needs MARKET_DATA_PROVIDER=simulated")
		}
		return avanza.NewMarketProvider(httpClient, cfg.Avanza.BaseURL), nil
	}

	// One shared instance: its daily-aggregate cache is per instance, and the first request for an
	// instrument walks its price path from the epoch, so warm every instrument once at startup.
	sim := simmarket.New(time.Now)
	go warmSimMarket(ctx, sim)
	log.Printf("market data provider: %s", config.MarketProviderSimulated)

	if !cfg.Market.SimControlEnabled {
		return sim, nil
	}
	log.Printf("WARNING: DEV/TEST ONLY: sim control is enabled; PUT/DELETE /api/v1/sim/instruments/{id}/price " +
		"are mounted without auth or rate limit. Never enable MARKET_SIM_CONTROL_ENABLED in production.")
	return sim, handlers.NewSimHandler(sim)
}

// warmSimMarket fills the simulated market's caches: the search matches every instrument ("SIM-" ids)
// and prices each one up to now.
func warmSimMarket(ctx context.Context, sim *simmarket.Market) {
	if ctx.Err() != nil {
		return
	}
	start := time.Now()
	items, err := sim.Search(ctx, "SIM")
	if err != nil {
		log.Printf("simulated market warm-up failed: %v", err)
		return
	}
	log.Printf("simulated market warmed: %d instruments in %s", len(items), time.Since(start).Round(time.Millisecond))
}

// newLLM builds the LLM provider selected by cfg.LLM.Provider. The fake answers deterministically and is
// dev/test only; the Anthropic adapter needs ANTHROPIC_API_KEY (without it every call fails with
// AI_UNAVAILABLE, but the rest of the API keeps working).
func newLLM(cfg *config.Config) domain.LLM {
	if cfg.LLM.Provider == config.LLMProviderFake {
		log.Printf("WARNING: DEV/TEST ONLY: LLM provider: fake (deterministic answers, no model calls)")
		return fake.New()
	}
	if cfg.LLM.AnthropicAPIKey == "" {
		log.Printf("WARNING: LLM provider anthropic without ANTHROPIC_API_KEY: AI features will answer 503 AI_UNAVAILABLE")
	}
	if _, ok := cfg.LLM.Prices[cfg.LLM.Model]; !ok {
		log.Printf("WARNING: no price for LLM model %q: its usage is recorded with cost 0", cfg.LLM.Model)
	}
	log.Printf("LLM provider: %s (model %s, timeout %s, daily budget %d tokens per user)",
		config.LLMProviderAnthropic, cfg.LLM.Model, cfg.LLM.Timeout, cfg.LLM.DailyTokenBudget)
	return anthropic.New(cfg.LLM.AnthropicAPIKey, cfg.LLM.Model, cfg.LLM.AnthropicBaseURL)
}
