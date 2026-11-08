// Package config loads with-frontend runtime configuration from environment variables.
package config

import (
	"os"

	"github.com/birdordev/plumego/core"
)

// Config holds all runtime configuration for the with-frontend reference app.
type Config struct {
	Core        core.AppConfig
	APIPrefix   string
	UIPrefix    string
	AssetsDir   string
	SPAFallback bool
}

// Load returns a Config populated from environment variables with safe defaults.
func Load() (Config, error) {
	return Config{
		Core:        coreDefaults(),
		APIPrefix:   envString("API_PREFIX", "/api"),
		UIPrefix:    envString("UI_PREFIX", "/"),
		AssetsDir:   envString("ASSETS_DIR", "./assets"),
		SPAFallback: true,
	}, nil
}

func coreDefaults() core.AppConfig {
	cfg := core.DefaultConfig()
	cfg.Addr = envString("APP_ADDR", ":8088")
	return cfg
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
