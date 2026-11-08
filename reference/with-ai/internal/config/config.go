// Package config loads with-ai runtime configuration from environment variables.
package config

import (
	"os"

	"github.com/birdordev/plumego/core"
)

// Config holds all runtime configuration for the with-ai reference app.
type Config struct {
	Core core.AppConfig
}

// Load returns a Config populated from environment variables with safe defaults.
func Load() (Config, error) {
	cfg := Config{Core: core.DefaultConfig()}
	cfg.Core.Addr = envString("APP_ADDR", ":8086")
	return cfg, nil
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
