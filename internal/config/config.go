// Package config loads application settings from environment variables.
//
// Why environment variables and not a config file? Secrets (the database
// password, the JWT signing key) must never be committed to git, and every
// deployment target — your laptop, CI, a container — can set env vars without
// shipping a different file. This is the "twelve-factor" convention.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Config holds every knob the application reads at startup.
//
// It is passed explicitly to the things that need it rather than being a
// package-level global. Globals make tests hard to write, because two tests
// running at once would fight over the same value.
type Config struct {
	// Addr is the TCP address the HTTP server binds to, e.g. ":8080".
	Addr string

	// DatabaseURL is a Postgres connection string.
	DatabaseURL string

	// Env is "development" or "production". Controls log formatting and
	// whether internal error details are exposed in HTTP responses.
	Env string

	// LogLevel is one of: debug, info, warn, error.
	LogLevel slog.Level

	// JWTSecret signs authentication tokens (Phase 6).
	JWTSecret string

	// ShutdownTimeout bounds how long we wait for in-flight requests to
	// finish when the process is asked to stop.
	ShutdownTimeout time.Duration
}

// Load reads configuration from the environment, applying defaults.
//
// It returns an error rather than calling os.Exit, so that tests can call it
// and main() stays in charge of how the program dies. Returning errors up to
// the caller instead of handling them in place is the central Go idiom.
func Load() (Config, error) {
	cfg := Config{
		Addr:            env("VIATUTA_ADDR", ":8080"),
		DatabaseURL:     env("VIATUTA_DATABASE_URL", "postgres://localhost:5432/viatuta?sslmode=disable"),
		Env:             env("VIATUTA_ENV", "development"),
		JWTSecret:       env("VIATUTA_JWT_SECRET", ""),
		ShutdownTimeout: 15 * time.Second,
	}

	lvl, err := parseLevel(env("VIATUTA_LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = lvl

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: VIATUTA_DATABASE_URL must be set")
	}

	// Fail loudly in production rather than silently signing tokens with an
	// empty key — that would let anyone forge a login.
	if cfg.Env == "production" && cfg.JWTSecret == "" {
		return Config{}, fmt.Errorf("config: VIATUTA_JWT_SECRET must be set when VIATUTA_ENV=production")
	}

	return cfg, nil
}

// IsProduction reports whether we are running in the production environment.
func (c Config) IsProduction() bool { return c.Env == "production" }

// env returns the value of key, or def when the variable is unset or empty.
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("config: unknown VIATUTA_LOG_LEVEL %q (want debug|info|warn|error)", s)
	}
}

// NewLogger builds the application logger from the config.
//
// slog is Go's standard structured logger: instead of formatting a sentence,
// you log a message plus key/value pairs, which machines can parse. In
// development we print human-readable text; in production, JSON.
func (c Config) NewLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: c.LogLevel}
	if c.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
