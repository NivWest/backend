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
	"backend/internal/integrations/avanza"
	"backend/internal/integrations/database"
	"backend/internal/integrations/simmarket"
	"backend/internal/repository"
	"backend/internal/services"
)

type App struct {
	HealthHandler  *handlers.HealthHandler
	OpenAPIHandler *handlers.OpenAPIHandler
	UserHandler    *handlers.UserHandler
	MarketHandler  *handlers.MarketHandler
	// MarketStreamHandler serves GET /market/stream (story NW-09) from QuoteHub.
	MarketStreamHandler *handlers.MarketStreamHandler
	AuthHandler         *handlers.AuthHandler
	PortfolioHandler    *handlers.PortfolioHandler
	OrderHandler        *handlers.OrderHandler
	WatchlistHandler    *handlers.WatchlistHandler
	AnalyticsHandler    *handlers.AnalyticsHandler
	AuthRepo            domain.AuthRepository

	// QuoteHub fans live quotes out to the streams. Close it on shutdown (cmd/api registers it with
	// http.Server.RegisterOnShutdown) so open streams end and the server can drain.
	QuoteHub *services.QuoteHub

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

	// Live quotes (story NW-09): one poller per watched instrument, shared by every stream.
	quoteHub := services.NewQuoteHub(provider, services.QuoteHubOptions{Interval: cfg.Market.StreamInterval})
	marketStreamHandler := handlers.NewMarketStreamHandler(quoteHub, handlers.StreamConfig{
		Heartbeat:    cfg.Market.StreamHeartbeat,
		WriteTimeout: cfg.Server.WriteTimeout,
		MaxPerIP:     cfg.Market.StreamMaxPerIP,
	})
	log.Printf("market stream: poll every %s, heartbeat %s, at most %d streams per IP",
		cfg.Market.StreamInterval, cfg.Market.StreamHeartbeat, cfg.Market.StreamMaxPerIP)

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

	// The matching engine (story NW-08) fills LIMIT and STOP orders. Every replica runs the loop, but only the
	// holder of the Postgres advisory lock runs cycles; the others retry every interval. It stops (and releases
	// the lock) when closeApp cancels its context.
	leaderLock, err := repository.NewAdvisoryLock(db, domain.MatchingEngineLockKey)
	if err != nil {
		cancelWarm()
		return nil, nil, err
	}
	engine := services.NewMatchingEngine(repository.NewMatchingRepository(db), provider, leaderLock, time.Now, services.DefaultMatchingWorkers)
	matchCtx, cancelMatch := context.WithCancel(context.Background())
	matchDone := make(chan struct{})
	go func() {
		defer close(matchDone)
		engine.Run(matchCtx, cfg.Matching.Interval)
	}()
	log.Printf("matching engine: every %s (leader election via pg_try_advisory_lock)", cfg.Matching.Interval)

	watchlistRepo := repository.NewWatchlistRepository(db)
	watchlistService := services.NewWatchlistService(watchlistRepo, provider)
	watchlistHandler := handlers.NewWatchlistHandler(watchlistService, authRepo)

	// The guest purger runs until closeApp cancels its context: once now, then every GuestPurgeInterval.
	guestRepo := repository.NewGuestRepository(db)
	purger := services.NewGuestPurger(guestRepo, domain.GuestRetention, time.Now)
	purgeCtx, cancelPurge := context.WithCancel(context.Background())
	go purger.Run(purgeCtx, domain.GuestPurgeInterval)

	snapshotCtx, cancelSnapshots := context.WithCancel(context.Background())
	go runSnapshotScheduler(snapshotCtx, newTimeTicker(cfg.Analytics.SnapshotInterval), snapshotService)
	log.Printf("equity snapshots every %s, risk-free rate %.4f", cfg.Analytics.SnapshotInterval, cfg.Analytics.RiskFreeRate)

	closeApp := func() error {
		quoteHub.Close()
		cancelMatch()
		<-matchDone // the engine releases its lock connection before the pool closes
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
		),
		OpenAPIHandler:      handlers.NewOpenAPIHandler(api.OpenAPISpec),
		UserHandler:         userHandler,
		MarketHandler:       marketHandler,
		MarketStreamHandler: marketStreamHandler,
		QuoteHub:            quoteHub,
		SimHandler:          simHandler,
		AuthHandler:         authHandler,
		PortfolioHandler:    portfolioHandler,
		OrderHandler:        orderHandler,
		WatchlistHandler:    watchlistHandler,
		AnalyticsHandler:    analyticsHandler,
		AuthRepo:            authRepo,
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
