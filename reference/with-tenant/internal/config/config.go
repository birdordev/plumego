// Package config loads with-tenant runtime configuration from environment variables.
package config

import (
	"os"

	"github.com/birdordev/plumego/core"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
)

// Config holds all runtime configuration for the with-tenant reference app.
type Config struct {
	Core         core.AppConfig
	TenantConfig *tenantcore.InMemoryConfigManager
	RateLimits   *tenantcore.InMemoryRateLimitManager
}

// Load returns a Config with in-memory tenant fixtures pre-seeded.
func Load() (Config, error) {
	tenantConfig := tenantcore.NewInMemoryConfigManager()
	tenantConfig.SetTenantConfig(tenantcore.Config{
		TenantID: "tenant-a",
		Policy: tenantcore.PolicyConfig{
			AllowedModels: []string{"gpt-4o"},
		},
		Quota: tenantcore.QuotaConfig{
			Limits: []tenantcore.QuotaLimit{
				{Window: tenantcore.QuotaWindowMinute, Requests: 100},
			},
		},
	})

	rateLimits := tenantcore.NewInMemoryRateLimitManager()
	rateLimits.SetRateLimit("tenant-a", tenantcore.RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             10,
	})

	cfg := Config{
		Core:         core.DefaultConfig(),
		TenantConfig: tenantConfig,
		RateLimits:   rateLimits,
	}
	cfg.Core.Addr = envString("APP_ADDR", ":8085")
	return cfg, nil
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
