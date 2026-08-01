// Package config loads Sprintly's runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port string
	Env  string

	// SupabaseURL is the project URL, e.g. https://abcd.supabase.co. Both the
	// PostgREST data endpoint and the JWKS endpoint are derived from it.
	SupabaseURL string

	// SupabaseAnonKey is public and only here so the API can report it; the
	// browser gets its own copy for sign-in.
	SupabaseAnonKey string

	// SupabaseServiceKey is the service role key. It bypasses RLS — it is the
	// credential the whole data layer runs on, and it must never reach a client.
	SupabaseServiceKey string

	SupabaseJWTSecret string // legacy HS256 secret; leave empty when JWKS applies
	JWTAudience       string

	AllowedOrigins []string
	ShutdownGrace  time.Duration
}

// Load reads the repository-root .env (if present) then the process
// environment. A missing .env is not an error — in production the values come
// from the platform.
func Load() (*Config, error) {
	_ = godotenv.Load(".env", "../.env")

	cfg := &Config{
		Port:               env("PORT", "8080"),
		Env:                env("APP_ENV", "development"),
		SupabaseURL:        strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseAnonKey:    os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseServiceKey: firstEnv("SUPABASE_SERVICE_ROLE_KEY", "SUPABASE_SERVICE_KEY", "SUPABASE_SECRET"),
		SupabaseJWTSecret:  os.Getenv("SUPABASE_JWT_SECRET"),
		JWTAudience:        env("SUPABASE_JWT_AUDIENCE", "authenticated"),
		AllowedOrigins:     splitList(env("ALLOWED_ORIGINS", "http://localhost:3000")),
		ShutdownGrace:      durationEnv("SHUTDOWN_GRACE", 15*time.Second),
	}

	if cfg.SupabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL is required (Supabase → Project Settings → API → Project URL)")
	}
	if cfg.SupabaseServiceKey == "" {
		return nil, fmt.Errorf("SUPABASE_SERVICE_ROLE_KEY is required — it is the credential the data layer runs on (Project Settings → API → service_role)")
	}
	return cfg, nil
}

// RESTURL is the PostgREST base the data layer talks to.
func (c *Config) RESTURL() string { return c.SupabaseURL + "/rest/v1" }

func (c *Config) IsProduction() bool { return c.Env == "production" }

// JWKSURL is where Supabase publishes the public keys for asymmetric (ES256/RS256)
// access tokens.
func (c *Config) JWKSURL() string {
	if c.SupabaseURL == "" {
		return ""
	}
	return c.SupabaseURL + "/auth/v1/.well-known/jwks.json"
}

// firstEnv returns the first of keys that is set, so the service key can be
// named any of the three things Supabase's docs and dashboard have called it.
func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
