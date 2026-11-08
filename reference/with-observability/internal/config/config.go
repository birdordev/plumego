// Package config loads and validates the with-observability application configuration.
package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
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
	EnvFile            string
	ServiceName        string   // APP_SERVICE_NAME; service identity in health and API responses.
	MaxBodyBytes       int64    // APP_MAX_BODY_BYTES; maximum request body size (0 disables).
	Version            string   // Build version injected via -ldflags "-X main.version=…".
	MetricsNamespace   string   // APP_METRICS_NAMESPACE; Prometheus metric name prefix.
	MetricsMaxSeries   int      // APP_METRICS_MAX_SERIES; maximum distinct label combinations retained.
	CORSAllowedOrigins []string // APP_CORS_ALLOWED_ORIGINS; comma-separated. Empty = ["*"]. Always restrict in production.
}

// Defaults returns safe configuration values for local development.
func Defaults() Config {
	coreCfg := core.DefaultConfig()
	coreCfg.Addr = ":8080"
	return Config{
		Core: coreCfg,
		App: AppConfig{
			EnvFile:          ".env",
			ServiceName:      "plumego-observability",
			MaxBodyBytes:     1 << 20, // 1 MiB
			Version:          "dev",
			MetricsNamespace: "plumego",
			MetricsMaxSeries: 10000,
		},
	}
}

// Load reads configuration from environment variables and flags.
func Load() (Config, error) {
	return load(os.Args, os.LookupEnv)
}

func load(args []string, lookupEnv func(string) (string, bool)) (Config, error) {
	cfg := Defaults()
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	cfg.App.EnvFile = resolveEnvFile(args, lookupEnv, cfg.App.EnvFile)
	fileEnv, err := readEnvFile(cfg.App.EnvFile)
	if err != nil {
		return cfg, err
	}

	applyEnvMap(&cfg, fileEnv)
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
	if cfg.App.MaxBodyBytes < 0 {
		return fmt.Errorf("APP_MAX_BODY_BYTES must be non-negative (0 disables the limit)")
	}
	if cfg.App.MetricsMaxSeries <= 0 {
		return fmt.Errorf("APP_METRICS_MAX_SERIES must be positive")
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

func applyEnv(cfg *Config, lookupEnv func(string) (string, bool)) {
	if lookupEnv == nil {
		return
	}
	str := func(key string, dest *string) {
		if val, ok := lookupEnv(key); ok {
			if v := strings.TrimSpace(val); v != "" {
				*dest = v
			}
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
	boolf := func(key string, dest *bool) {
		if val, ok := lookupEnv(key); ok {
			if b, err := strconv.ParseBool(strings.TrimSpace(val)); err == nil {
				*dest = b
			}
		}
	}
	str("APP_ADDR", &cfg.Core.Addr)
	str("APP_ENV_FILE", &cfg.App.EnvFile)
	str("APP_SERVICE_NAME", &cfg.App.ServiceName)
	int64f("APP_MAX_BODY_BYTES", &cfg.App.MaxBodyBytes)
	str("APP_METRICS_NAMESPACE", &cfg.App.MetricsNamespace)
	intf("APP_METRICS_MAX_SERIES", &cfg.App.MetricsMaxSeries)
	boolf("APP_TLS_ENABLED", &cfg.Core.TLS.Enabled)
	str("APP_TLS_CERT_FILE", &cfg.Core.TLS.CertFile)
	str("APP_TLS_KEY_FILE", &cfg.Core.TLS.KeyFile)
	if val, ok := lookupEnv("APP_CORS_ALLOWED_ORIGINS"); ok {
		if v := strings.TrimSpace(val); v != "" {
			parts := strings.Split(v, ",")
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
	}
}

func applyEnvMap(cfg *Config, values map[string]string) {
	if len(values) == 0 {
		return
	}
	applyEnv(cfg, func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	})
}

func applyFlags(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("with-observability", flag.ContinueOnError)
	fs.StringVar(&cfg.Core.Addr, "addr", cfg.Core.Addr, "listen address")
	fs.StringVar(&cfg.App.EnvFile, "env-file", cfg.App.EnvFile, "path to .env file")
	if len(args) == 0 {
		return fs.Parse(nil)
	}
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

func resolveEnvFile(args []string, lookupEnv func(string) (string, bool), defaultPath string) string {
	if envPath, ok := lookupEnv("APP_ENV_FILE"); ok && strings.TrimSpace(envPath) != "" {
		defaultPath = envPath
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--env-file" || arg == "-env-file" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(arg, "--env-file=") {
			return strings.TrimPrefix(arg, "--env-file=")
		}
		if strings.HasPrefix(arg, "-env-file=") {
			return strings.TrimPrefix(arg, "-env-file=")
		}
	}
	return defaultPath
}

func readEnvFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		values[key] = value
	}
	return values, scanner.Err()
}

func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(line[idx+1:])
	if len(value) >= 2 {
		q := value[0]
		if (q == '"' || q == '\'') && value[len(value)-1] == q {
			value = value[1 : len(value)-1]
			value = strings.ReplaceAll(value, string([]byte{'\\', q}), string(q))
		}
	}
	return key, value, true
}
