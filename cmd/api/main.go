package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/app"
	"backend/internal/config"
	"github.com/gin-gonic/gin"
)

// shutdownTimeout bounds how long in-flight requests may finish after SIGINT/SIGTERM.
const shutdownTimeout = 10 * time.Second

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

	// Graceful shutdown (story NW-08): on SIGINT/SIGTERM stop accepting requests, let in-flight ones finish,
	// then closeApp stops the background workers (the matching engine releases its leader lock) and the pool.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = closeApp()
			log.Fatal(err)
		}
	case <-ctx.Done():
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}

	if err := closeApp(); err != nil {
		log.Fatal(err)
	}
}
