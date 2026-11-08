package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/app"
	"backend/internal/config"
	"github.com/gin-gonic/gin"
)

// shutdownTimeout bounds how long SIGINT/SIGTERM waits for in-flight requests before the server exits.
const shutdownTimeout = 15 * time.Second

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatal(err)
	}

	app, closeApp, err := app.NewApp(cfg)
	if err != nil {
		log.Fatal(err)
	}

	engine := app.Router(gin.Logger(), gin.Recovery())

	server := &http.Server{
		Addr:         cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}
	// Streams never go idle on their own: closing the hub ends them (each gets an `error` event), so Shutdown
	// can drain the remaining connections (story NW-09).
	server.RegisterOnShutdown(app.QuoteHub.Close)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			_ = closeApp()
			log.Fatal(err)
		}
	case <-ctx.Done():
		log.Printf("shutting down (waiting up to %s for open requests)", shutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		cancel()
	}

	if err := closeApp(); err != nil {
		log.Fatal(err)
	}
}
