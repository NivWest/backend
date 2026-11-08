package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/app"
	"backend/internal/config"
	"backend/internal/observability"
	"github.com/gin-gonic/gin"
)

// shutdownTimeout bounds how long SIGINT/SIGTERM waits for in-flight requests before the server exits.
const shutdownTimeout = 15 * time.Second

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := observability.NewLogger(os.Stdout, cfg.Observability.LogLevel, cfg.Observability.LogFormat)
	slog.SetDefault(logger)

	shutdownTracer, err := observability.InitTracer("tradeedu-api", cfg.Observability.OtelEndpoint)
	if err != nil {
		slog.Error("failed to initialize tracer", "error", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracer(ctx)
	}()

	appInstance, closeApp, err := app.NewApp(cfg)
	if err != nil {
		slog.Error("failed to initialize app", "error", err)
		os.Exit(1)
	}

	engine := appInstance.Router(
		observability.RequestLogger(logger),
		observability.HTTPMetrics(appInstance.Metrics),
		gin.Recovery(),
	)

	server := &http.Server{
		Addr:         cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}
	// Streams never go idle on their own: closing the hub ends them (each gets an `error` event), so Shutdown
	// can drain the remaining connections (story NW-09).
	server.RegisterOnShutdown(appInstance.QuoteHub.Close)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			_ = closeApp()
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		slog.Info("shutting down (waiting for open requests)", "timeout", shutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown failed", "error", err)
		}
		cancel()
	}

	if err := closeApp(); err != nil {
		slog.Error("failed to close app", "error", err)
		os.Exit(1)
	}
}
