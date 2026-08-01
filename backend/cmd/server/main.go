// Command server runs the Sprintly API.
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

	"github.com/sprintly/sprintly/backend/internal/api"
	"github.com/sprintly/sprintly/backend/internal/config"
	"github.com/sprintly/sprintly/backend/internal/db"
	"github.com/sprintly/sprintly/backend/internal/realtime"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogging(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	hub := realtime.NewHub()
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: api.NewServer(cfg, database, hub).Routes(),
		// No WriteTimeout: it would sever long-lived WebSocket connections.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("sprintly api listening", "addr", srv.Addr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		slog.Info("shutting down", "grace", cfg.ShutdownGrace)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func setupLogging(cfg *config.Config) {
	level := slog.LevelDebug
	var handler slog.Handler

	if cfg.IsProduction() {
		level = slog.LevelInfo
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))
}
