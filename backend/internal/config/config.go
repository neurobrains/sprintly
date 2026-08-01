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
	Port        string
	Env         string
	DatabaseURL string

	// SupabaseURL is the project URL, e.g. https://abcd.supabase.co. The JWKS
	// endpoint for verifying access tokens is derived from it.
	SupabaseURL       string
	SupabaseAnonKey   string
	SupabaseJWTSecret string // legacy HS256 secret; optional once JWKS is in use
	JWTAudience       string

	AllowedOrigins []string
	ShutdownGrace  time.Duration
}

// Load reads .env (if present) then the process environment. A missing .env is
// not an error — in production the values come from the platform.
func Load() (*Config, error) {
	_ = godotenv.Load(".env", "../.env")

	cfg := &Config{
		Port:              env("PORT", "8080"),
		Env:               env("APP_ENV", "development"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		SupabaseURL:       strings.TrimRight(os.Getenv("SUPABASE_URL"), "/"),
		SupabaseAnonKey:   os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseJWTSecret: os.Getenv("SUPABASE_JWT_SECRET"),
		JWTAudience:       env("SUPABASE_JWT_AUDIENCE", "authenticated"),
		AllowedOrigins:    splitList(env("ALLOWED_ORIGINS", "http://localhost:3000")),
		ShutdownGrace:     durationEnv("SHUTDOWN_GRACE", 15*time.Second),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required (Supabase → Project Settings → Database → Connection string)")
	}
	if cfg.SupabaseURL == "" && cfg.SupabaseJWTSecret == "" {
		return nil, fmt.Errorf("set SUPABASE_URL (for JWKS) or SUPABASE_JWT_SECRET (legacy HS256) so access tokens can be verified")
	}
	return cfg, nil
}

func (c *Config) IsProduction() bool { return c.Env == "production" }

// JWKSURL is where Supabase publishes the public keys for asymmetric (ES256/RS256)
// access tokens.
func (c *Config) JWKSURL() string {
	if c.SupabaseURL == "" {
		return ""
	}
	return c.SupabaseURL + "/auth/v1/.well-known/jwks.json"
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
