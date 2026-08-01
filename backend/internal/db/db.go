// Package db owns the Postgres connection pool and translates driver errors
// into the API's error vocabulary.
package db

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sprintly/sprintly/backend/internal/httpx"
)

type DB struct{ *pgxpool.Pool }

func Connect(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// Supabase's pooler (port 6543) is a transaction pooler and cannot hold
	// server-side prepared statements across checkouts.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{pool}, nil
}

// InTx runs fn inside a transaction, rolling back on error or panic.
func (d *DB) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return err
	}
	return tx.Commit(ctx)
}

// MapError converts a pgx/Postgres error into an *httpx.Error. The RPCs in
// migration 007 raise domain failures with deliberate SQLSTATEs, which is what
// the switch below keys on.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case "23505": // unique_violation
		return &httpx.Error{Status: http.StatusConflict, Code: "conflict",
			Message: friendlyUnique(pgErr.ConstraintName), Detail: pgErr.ConstraintName}
	case "23503": // foreign_key_violation
		return httpx.Errorf(http.StatusBadRequest, "invalid_reference",
			"That reference does not exist")
	case "23514": // check_violation
		return httpx.Errorf(http.StatusBadRequest, "invalid", "%s", pgErr.Message)
	case "42501": // insufficient_privilege — raised by our RPCs
		return &httpx.Error{Status: http.StatusForbidden, Code: "forbidden", Message: pgErr.Message}
	case "P0002": // no_data_found — raised by our RPCs
		return &httpx.Error{Status: http.StatusNotFound, Code: "not_found", Message: pgErr.Message}
	case "23502": // not_null_violation
		return httpx.Errorf(http.StatusBadRequest, "missing_field",
			"%s is required", pgErr.ColumnName)
	}

	// RAISE EXCEPTION without an errcode lands here as P0001.
	if pgErr.Code == "P0001" {
		return httpx.Errorf(http.StatusBadRequest, "invalid", "%s", pgErr.Message)
	}
	return err
}

func friendlyUnique(constraint string) string {
	switch constraint {
	case "workspaces_slug_key":
		return "That workspace URL is taken"
	case "workspaces_join_code_key":
		return "Could not allocate a join code, please retry"
	case "projects_workspace_id_key_key":
		return "A project with that key already exists"
	case "workspace_members_pkey":
		return "Already a member of this workspace"
	case "time_entries_one_running_idx":
		return "You already have a timer running"
	case "labels_workspace_id_name_key":
		return "A label with that name already exists"
	case "channels_workspace_id_name_key":
		return "A channel with that name already exists"
	default:
		return "That already exists"
	}
}
