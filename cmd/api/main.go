package main

import (
	"backend/internal/app"
	"backend/internal/config"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

type Registrar interface {
	Register(v1 *gin.RouterGroup)
}

// Mount accepts a variadic list of Registrar interfaces and registers them.
func mountHandlers(engine *gin.Engine, handlers ...Registrar) {
	v1 := engine.Group("/api/v1")
	for _, handler := range handlers {
		handler.Register(v1)
	}
}

func newHTTPServer(app *app.App) (*gin.Engine, error) {
	engine := gin.New()

	engine.Use(
		gin.Logger(),
		gin.Recovery(),
	)

	mountHandlers(
		engine,
		app.StockHandler,
		app.UserHandler,
		app.AuthHandler,
		app.PortfolioHandler,
		app.OrderHandler,
		app.WatchlistHandler,
	)

	return engine, nil
}

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatal(err)
	}

	app, closeApp, err := app.NewApp(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := closeApp(); err != nil {
			log.Fatal(err)
		}
	}()

	engine, err := newHTTPServer(app)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:         cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	log.Fatal(server.ListenAndServe())
}
