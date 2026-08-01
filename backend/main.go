// Command sprintly runs the Sprintly API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sprintly/sprintly/backend/config"
	"github.com/sprintly/sprintly/backend/db"
	"github.com/sprintly/sprintly/backend/handlers"
	"github.com/sprintly/sprintly/backend/realtime"
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

	var data *db.Client
	if cfg.DelegatedAuth() {
		// No service key: PostgREST runs as the caller, under RLS.
		data = db.NewDelegated(cfg.SupabaseURL, cfg.SupabaseAnonKey)
		slog.Warn("no service key configured — running in delegated mode; "+
			"row level security is enforcing access, not just the API",
			"hint", "set SUPABASE_SERVICE_ROLE_KEY to switch")
	} else {
		data = db.New(cfg.SupabaseURL, cfg.SupabaseServiceKey)
	}

	// Fail fast on a bad URL or key rather than serving 502s to every caller.
	pingCtx, cancelPing := context.WithTimeout(ctx, 10*time.Second)
	err = data.Ping(pingCtx)
	cancelPing()
	if err != nil {
		return fmt.Errorf("reach Supabase at %s: %w", cfg.RESTURL(), err)
	}

	hub := realtime.NewHub()
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handlers.NewServer(cfg, data, hub).Routes(),
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
