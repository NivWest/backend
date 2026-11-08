package main

import (
	"backend/internal/app"
	"backend/internal/config"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

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

	engine := app.Router(gin.Logger(), gin.Recovery())

	server := &http.Server{
		Addr:         cfg.Server.Host + ":" + cfg.Server.Port,
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	log.Fatal(server.ListenAndServe())
}
