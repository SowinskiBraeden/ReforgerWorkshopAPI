package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api/handlers"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/util"
	"go.uber.org/zap"
)

const version string = "1.1.0"

func main() {
	a := handlers.App{}
	a.Config = *config.New()
	util.ConfigureScraper(util.ScraperConfig{
		Timeout:     a.Config.UpstreamTimeout,
		Retries:     a.Config.UpstreamRetries,
		Concurrency: a.Config.UpstreamConcurrency,
		UserAgent:   a.Config.UpstreamUserAgent,
	})
	a.Initialize() // Initialize router

	zap.S().Infow(fmt.Sprintf("ReforgerWorkshopAPI v%s is up and running", version), "url", a.Config.BaseURL, "bind_address", a.Config.BindAddress)
	server := &http.Server{
		Addr:              a.Config.BindAddress,
		Handler:           a.Router,
		ReadHeaderTimeout: a.Config.ReadHeaderTimeout,
		ReadTimeout:       a.Config.ReadTimeout,
		WriteTimeout:      a.Config.WriteTimeout,
		IdleTimeout:       a.Config.IdleTimeout,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		zap.S().Warnw("http server shutdown did not complete cleanly", "error", err)
	}

	if err := a.Shutdown(ctx); err != nil {
		zap.S().Warnw("application shutdown did not complete cleanly", "error", err)
	}
	zap.S().Info("server shut down")
}
