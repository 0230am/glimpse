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

	"github.com/0230am/glimpse/internal/api"
	"github.com/0230am/glimpse/internal/config"
	"github.com/0230am/glimpse/internal/fetcher"
	"github.com/0230am/glimpse/internal/service"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	configuration, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	fetchClient := fetcher.New(fetcher.Config{
		MaximumConcurrentRequests:        configuration.MaximumConcurrentRequests,
		MaximumConcurrentRequestsPerHost: configuration.MaximumConcurrentRequestsPerHost,
		AllowedPorts:                     configuration.AllowedPorts,
	})
	handler := api.New(service.New(fetchClient), configuration.PublicURL, configuration.AllowedOrigins, logger)
	server := &http.Server{
		Addr:              configuration.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-shutdownContext.Done()
		context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(context); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("Glimpse listening", "address", configuration.ListenAddress, "public_url", configuration.PublicURL.String())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
