// Package config loads and validates Nabu's configuration from environment
// variables, once, at startup. Missing or malformed required values fail fast
// here rather than mid-request.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	// Port the HTTP server listens on. PORT env var, default 8080.
	Port int
	// DatabaseURL is the Postgres connection string. DATABASE_URL env var, required.
	DatabaseURL string
	// InitialAdminEmail and InitialAdminPassword seed the first account when
	// the users table is empty. NABU_INITIAL_ADMIN_EMAIL /
	// NABU_INITIAL_ADMIN_PASSWORD env vars — optional, but set together.
	InitialAdminEmail    string
	InitialAdminPassword string
	// AuthSecret is the HS256 signing key for access tokens (ADR-0003).
	// NABU_AUTH_SECRET env var, required, at least 32 bytes.
	AuthSecret string
	// CookieSecure sets the Secure flag on auth cookies. NABU_COOKIE_SECURE
	// env var, default true; set false only for plain-HTTP local development.
	CookieSecure bool
}

// minAuthSecretLen is the floor for NABU_AUTH_SECRET: a 256-bit key matches
// the HS256 output size, below which the shared secret weakens signing.
const minAuthSecretLen = 32

func Load() (*Config, error) {
	cfg := &Config{Port: 8080}

	if v := os.Getenv("PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("parsing PORT %q: must be an integer between 1 and 65535", v)
		}
		cfg.Port = port
	}

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	cfg.InitialAdminEmail = os.Getenv("NABU_INITIAL_ADMIN_EMAIL")
	cfg.InitialAdminPassword = os.Getenv("NABU_INITIAL_ADMIN_PASSWORD")
	if (cfg.InitialAdminEmail == "") != (cfg.InitialAdminPassword == "") {
		return nil, fmt.Errorf("NABU_INITIAL_ADMIN_EMAIL and NABU_INITIAL_ADMIN_PASSWORD must be set together")
	}

	cfg.AuthSecret = os.Getenv("NABU_AUTH_SECRET")
	if len(cfg.AuthSecret) < minAuthSecretLen {
		return nil, fmt.Errorf("NABU_AUTH_SECRET is required and must be at least %d bytes", minAuthSecretLen)
	}

	// Default true: a missing var must not silently drop the Secure flag in
	// production. Only an explicit false opts out.
	cfg.CookieSecure = true
	if v := os.Getenv("NABU_COOKIE_SECURE"); v != "" {
		secure, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("parsing NABU_COOKIE_SECURE %q: must be a boolean", v)
		}
		cfg.CookieSecure = secure
	}

	return cfg, nil
}
