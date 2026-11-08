// Package config loads with-ops runtime configuration from environment variables.
package config

import (
	"os"

	"github.com/birdordev/plumego/core"
)

// Config holds all runtime configuration for the with-ops reference app.
type Config struct {
	Core        core.AppConfig
	OpsEnabled  bool
	OpsToken    string
	OpsBasePath string
}

// Load returns a Config populated from environment variables with safe defaults.
func Load() (Config, error) {
	cfg := Config{
		Core:        core.DefaultConfig(),
		OpsEnabled:  true,
		OpsToken:    envString("OPS_TOKEN", "local-admin-token"),
		OpsBasePath: envString("OPS_BASE_PATH", "/ops"),
	}
	cfg.Core.Addr = envString("APP_ADDR", ":8087")
	return cfg, nil
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
