// Package config loads production reference configuration.
package config

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/core"
)

// Config holds all application configuration.
type Config struct {
	Core core.AppConfig
	App  AppConfig
}

// AppConfig holds app-local production profile settings.
type AppConfig struct {
	Environment      string
	ServiceName      string
	APIToken         string
	OpsToken         string
	ProfileStorePath string
	BodyLimitBytes   int64
	RequestTimeout   time.Duration
	RateLimit        float64
	RateBurst        int
}

// Defaults returns conservative local defaults for the production reference.
func Defaults() Config {
	coreCfg := core.DefaultConfig()
	coreCfg.Addr = ":8080"
	return Config{
		Core: coreCfg,
		App: AppConfig{
			Environment:    "local",
			ServiceName:    "plumego-production-reference",
			BodyLimitBytes: 1 << 20,
			RequestTimeout: 5 * time.Second,
			RateLimit:      20,
			RateBurst:      40,
		},
	}
}

// Load reads environment variables and command-line flags.
func Load() (Config, error) {
	return load(os.Args, os.LookupEnv)
}

func load(args []string, lookupEnv func(string) (string, bool)) (Config, error) {
	cfg := Defaults()
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	applyEnv(&cfg, lookupEnv)
	if err := applyFlags(&cfg, args); err != nil {
		return cfg, err
	}
	return cfg, Validate(cfg)
}

// Validate returns an error if cfg is unusable.
func Validate(cfg Config) error {
	if cfg.Core.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	if cfg.App.ServiceName == "" {
		return fmt.Errorf("service name is required")
	}
	if cfg.App.Environment == "" {
		return fmt.Errorf("environment is required")
	}
	if cfg.App.BodyLimitBytes <= 0 {
		return fmt.Errorf("body limit bytes must be positive")
	}
	if cfg.App.RequestTimeout <= 0 {
		return fmt.Errorf("request timeout must be positive")
	}
	if cfg.App.RateLimit <= 0 {
		return fmt.Errorf("rate limit must be positive")
	}
	if cfg.App.RateBurst <= 0 {
		return fmt.Errorf("rate burst must be positive")
	}
	if cfg.Core.TLS.Enabled {
		if cfg.Core.TLS.CertFile == "" {
			return fmt.Errorf("APP_TLS_CERT_FILE is required when TLS is enabled")
		}
		if cfg.Core.TLS.KeyFile == "" {
			return fmt.Errorf("APP_TLS_KEY_FILE is required when TLS is enabled")
		}
	}
	return nil
}

// applyEnv is the single source of truth for mapping APP_* variables to config
// fields. It accepts a lookupEnv function so it works identically with
// os.LookupEnv, test stubs, and t.Setenv in integration tests.
func applyEnv(cfg *Config, lookupEnv func(string) (string, bool)) {
	if lookupEnv == nil {
		return
	}
	str := func(key string, dest *string) {
		if val, ok := lookupEnv(key); ok && strings.TrimSpace(val) != "" {
			*dest = strings.TrimSpace(val)
		}
	}
	int64f := func(key string, dest *int64) {
		if val, ok := lookupEnv(key); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil {
				*dest = n
			}
		}
	}
	intf := func(key string, dest *int) {
		if val, ok := lookupEnv(key); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
				*dest = n
			}
		}
	}
	floatf := func(key string, dest *float64) {
		if val, ok := lookupEnv(key); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
				*dest = f
			}
		}
	}
	durf := func(key string, dest *time.Duration) {
		if val, ok := lookupEnv(key); ok {
			if d, err := time.ParseDuration(strings.TrimSpace(val)); err == nil {
				*dest = d
			}
		}
	}
	boolf := func(key string, dest *bool) {
		if val, ok := lookupEnv(key); ok {
			if b, err := strconv.ParseBool(strings.TrimSpace(val)); err == nil {
				*dest = b
			}
		}
	}
	str("APP_ADDR", &cfg.Core.Addr)
	str("APP_ENV", &cfg.App.Environment)
	str("APP_SERVICE_NAME", &cfg.App.ServiceName)
	str("APP_API_TOKEN", &cfg.App.APIToken)
	str("OPS_TOKEN", &cfg.App.OpsToken)
	str("APP_PROFILE_STORE_PATH", &cfg.App.ProfileStorePath)
	int64f("APP_BODY_LIMIT_BYTES", &cfg.App.BodyLimitBytes)
	durf("APP_REQUEST_TIMEOUT", &cfg.App.RequestTimeout)
	floatf("APP_RATE_LIMIT", &cfg.App.RateLimit)
	intf("APP_RATE_BURST", &cfg.App.RateBurst)
	boolf("APP_TLS_ENABLED", &cfg.Core.TLS.Enabled)
	str("APP_TLS_CERT_FILE", &cfg.Core.TLS.CertFile)
	str("APP_TLS_KEY_FILE", &cfg.Core.TLS.KeyFile)
}

func applyFlags(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("production-service", flag.ContinueOnError)
	fs.StringVar(&cfg.Core.Addr, "addr", cfg.Core.Addr, "listen address")
	fs.StringVar(&cfg.App.Environment, "env", cfg.App.Environment, "deployment environment")
	fs.StringVar(&cfg.App.ServiceName, "service-name", cfg.App.ServiceName, "service name")
	fs.StringVar(&cfg.App.APIToken, "api-token", cfg.App.APIToken, "bearer token for protected API routes")
	fs.StringVar(&cfg.App.OpsToken, "ops-token", cfg.App.OpsToken, "bearer token for ops routes")
	fs.StringVar(&cfg.App.ProfileStorePath, "profile-store-path", cfg.App.ProfileStorePath, "optional JSON profile store path")
	fs.Int64Var(&cfg.App.BodyLimitBytes, "body-limit-bytes", cfg.App.BodyLimitBytes, "request body limit")
	fs.DurationVar(&cfg.App.RequestTimeout, "request-timeout", cfg.App.RequestTimeout, "request timeout")
	fs.Float64Var(&cfg.App.RateLimit, "rate-limit", cfg.App.RateLimit, "requests per second per client")
	fs.IntVar(&cfg.App.RateBurst, "rate-burst", cfg.App.RateBurst, "rate-limit burst capacity")
	if len(args) == 0 {
		return fs.Parse(nil)
	}
	// Collect registered flag names from the FlagSet itself so filterFlagArgs
	// stays in sync automatically when new flags are added above.
	known := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) { known[f.Name] = true })
	return fs.Parse(filterFlagArgs(args[1:], known))
}

func filterFlagArgs(args []string, known map[string]bool) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, hasValue := flagName(arg)
		if !known[name] {
			continue
		}
		out = append(out, arg)
		if !hasValue && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return out
}

func flagName(arg string) (name string, hasValue bool) {
	if strings.HasPrefix(arg, "--") {
		arg = strings.TrimPrefix(arg, "--")
	} else if strings.HasPrefix(arg, "-") {
		arg = strings.TrimPrefix(arg, "-")
	} else {
		return "", false
	}
	if idx := strings.IndexByte(arg, '='); idx >= 0 {
		return arg[:idx], true
	}
	return arg, false
}
