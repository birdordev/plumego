// Package config loads the with-rest demo configuration.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/birdordev/plumego/core"
)

// Config holds all application configuration.
type Config struct {
	Core core.AppConfig
	App  AppConfig
}

// AppConfig holds app-local, non-kernel configuration.
type AppConfig struct {
	CORSAllowedOrigins []string // APP_CORS_ALLOWED_ORIGINS; comma-separated. Empty = ["*"]. Always restrict in production.
}

// Defaults returns safe configuration values for local development.
func Defaults() Config {
	coreCfg := core.DefaultConfig()
	coreCfg.Addr = ":8084"
	return Config{Core: coreCfg}
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	cfg := Defaults()
	if addr := os.Getenv("APP_ADDR"); addr != "" {
		cfg.Core.Addr = addr
	}
	if val := os.Getenv("APP_CORS_ALLOWED_ORIGINS"); val != "" {
		parts := strings.Split(val, ",")
		origins := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				origins = append(origins, s)
			}
		}
		if len(origins) > 0 {
			cfg.App.CORSAllowedOrigins = origins
		}
	}
	return cfg, Validate(cfg)
}

// Validate returns an error if cfg is unusable.
func Validate(cfg Config) error {
	if cfg.Core.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	return nil
}
