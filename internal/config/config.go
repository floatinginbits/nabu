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
}

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

	return cfg, nil
}
