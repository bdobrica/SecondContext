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

	"github.com/bdobrica/SecondContext/internal/api"
	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	logger := newLogger(cfg)
	logger.Info("starting service", "addr", cfg.HTTP.Addr, "postgres_enabled", cfg.Postgres.Enabled)

	dbPool, err := db.Open(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close(dbPool)

	handler := api.NewServer(cfg, logger, dbPool)
	httpServer := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", cfg.HTTP.Addr)
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
		defer cancel()

		logger.Info("shutting down")
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown server", "error", err)
			os.Exit(1)
		}
	}

	logger.Info("service stopped")
}

func newLogger(cfg config.Config) *slog.Logger {
	options := &slog.HandlerOptions{Level: cfg.Log.Level}
	handler := slog.NewJSONHandler(os.Stdout, options)

	return slog.New(handler).With(
		"service", cfg.App.Name,
		"environment", cfg.App.Env,
	)
}
