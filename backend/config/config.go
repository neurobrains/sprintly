// Package config loads Sprintly's runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	// SupabaseAnonKey is public — it also ships in the browser bundle. When no
	// service key is configured it becomes the API's PostgREST apikey, paired
	// with the caller's own access token.
	SupabaseAnonKey string

	// SupabaseServiceKey is the service role (or "secret") key. Optional.
	//
	// Set   -> the API authenticates to PostgREST as the service role, which
	//          bypasses RLS. The Go API is then the only authorization boundary.
	// Unset -> the API falls back to the anon key plus the caller's own access
	//          token, so PostgREST runs every statement as that user and RLS
	//          enforces access. See Config.DelegatedAuth.
	SupabaseServiceKey string

	SupabaseJWTSecret string // legacy HS256 secret; leave empty when JWKS applies
	JWTAudience       string

	AllowedOrigins []string
	ShutdownGrace  time.Duration
}

// Load reads the nearest .env (if present) then the process environment. A
// missing .env is not an error — in production the values come from the
// platform.
func Load() (*Config, error) {
	loadDotEnv()

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
	if cfg.SupabaseServiceKey == "" && cfg.SupabaseAnonKey == "" {
		return nil, fmt.Errorf("set SUPABASE_SERVICE_ROLE_KEY, or SUPABASE_ANON_KEY to run in delegated mode (Project Settings → API)")
	}
	return cfg, nil
}

// DelegatedAuth reports whether the API forwards the caller's access token to
// PostgREST instead of holding a service key. In that mode RLS — not the Go
// middleware alone — decides what each request can reach.
func (c *Config) DelegatedAuth() bool { return c.SupabaseServiceKey == "" }

// APIKey is the value PostgREST wants in the apikey header.
func (c *Config) APIKey() string {
	if c.SupabaseServiceKey != "" {
		return c.SupabaseServiceKey
	}
	return c.SupabaseAnonKey
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

// loadDotEnv walks up from the working directory and loads the first .env it
// finds, so `go run .` works from backend/ and from the repository root alike.
//
// It deliberately does not use godotenv.Load's variadic form: that returns on
// the first filename that does not exist, so a missing backend/.env would stop
// it ever reaching the repository root — which read as "SUPABASE_URL is
// required" even though the file was sitting right there.
func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for range 4 { // backend/ -> repo root is one hop; three spare
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			// Values already in the real environment win, which is what
			// godotenv does and what a container expects.
			_ = godotenv.Load(candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
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
