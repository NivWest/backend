package app

import (
	"backend/internal/config"
	"backend/internal/domain"
	"backend/internal/handlers"
	"backend/internal/integrations/avanza"
	"backend/internal/integrations/database"
	"backend/internal/repository"
	"backend/internal/services"
	"fmt"
	"net/http"
)

type App struct {
	UserHandler      *handlers.UserHandler
	StockHandler     *handlers.StockHandler
	AuthHandler      *handlers.AuthHandler
	PortfolioHandler *handlers.PortfolioHandler
	OrderHandler     *handlers.OrderHandler
	WatchlistHandler *handlers.WatchlistHandler
	AuthRepo         domain.AuthRepository
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

	avanzaClient := avanza.NewClient(httpClient)
	stockService := services.NewStockService(avanzaClient)
	stockHandler := handlers.NewStockHandler(stockService)

	portfolioRepo := repository.NewPortfolioRepository(db)
	portfolioService := services.NewPortfolioService(portfolioRepo, avanzaClient, userRepo)
	portfolioHandler := handlers.NewPortfolioHandler(portfolioService, authRepo)

	orderRepo := repository.NewOrderRepository(db)
	orderService := services.NewOrderService(orderRepo, avanzaClient)
	orderHandler := handlers.NewOrderHandler(orderService, authRepo)

	watchlistRepo := repository.NewWatchlistRepository(db)
	watchlistService := services.NewWatchlistService(watchlistRepo)
	watchlistHandler := handlers.NewWatchlistHandler(watchlistService, authRepo)

	closeApp := func() error {
		sqlDB, err := db.DB()
		if err != nil {
			return fmt.Errorf("get sql database: %w", err)
		}
		return sqlDB.Close()
	}

	return &App{
		UserHandler:      userHandler,
		StockHandler:     stockHandler,
		AuthHandler:      authHandler,
		PortfolioHandler: portfolioHandler,
		OrderHandler:     orderHandler,
		WatchlistHandler: watchlistHandler,
		AuthRepo:         authRepo,
	}, closeApp, nil
}
