package app

import (
	"backend/internal/config"
	"backend/internal/integrations/database"
	"backend/internal/handlers"
	"backend/internal/integrations/avanza"
	"backend/internal/repository"
	"backend/internal/services"
	"net/http"
	"fmt"
)

type App struct {
	UserHandler  *handlers.UserHandler
	StockHandler *handlers.StockHandler
}

func NewApp(cfg *config.Config) (*App, func() error, error) {
	db, err := database.NewPostgresDB(cfg)
	if err != nil {
		return nil, nil, err
	}

	if err := database.Migrate(db); err != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, nil,fmt.Errorf("get sql database: %w", err)
		}
		_ = sqlDB.Close()
		return nil, nil, err
	}

	userRepo := repository.NewUserRepository(db)
	userService := services.NewUserService(userRepo)
	userHandler := handlers.NewUserHandler(userService)

	httpClient := &http.Client{
		Timeout: cfg.Avanza.Timeout,
	}

	avanzaClient := avanza.NewClient(httpClient)
	stockService := services.NewStockService(avanzaClient)
	stockHandler := handlers.NewStockHandler(stockService)

	closeApp := func() error {
		sqlDB, err := db.DB()
		if err != nil {
			return fmt.Errorf("get sql database: %w", err)
		}
		return sqlDB.Close()
	}

	return &App{
		UserHandler:  userHandler,
		StockHandler: stockHandler,
	}, closeApp, nil
}