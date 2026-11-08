// Package config loads and validates the reference application configuration
// from environment variables and command-line flags.
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
// Add application-specific fields here; keep framework/kernel config in Config.Core.
type AppConfig struct {
	EnvFile            string
	ServiceName        string   // APP_SERVICE_NAME; used as the service identity in health and API responses.
	MaxBodyBytes       int64    // APP_MAX_BODY_BYTES; maximum request body size. 0 disables the limit.
	WriteKey           string   // APP_WRITE_KEY; when non-empty, POST/PUT/DELETE /api/v1/items require X-Write-Key header. Empty disables the guard.
	Version            string   // Build version injected via -ldflags "-X main.version=…" in main.go; defaults to "dev".
	CORSAllowedOrigins []string // APP_CORS_ALLOWED_ORIGINS; comma-separated list of allowed origins. Empty defaults to ["*"] (allow all). Always restrict in production.
}

// Defaults returns safe configuration values for local development.
func Defaults() Config {
	coreCfg := core.DefaultConfig()
	coreCfg.Addr = ":8080"
	return Config{
		Core: coreCfg,
		App: AppConfig{
			EnvFile:      ".env",
			ServiceName:  "plumego-reference",
			MaxBodyBytes: 1 << 20, // 1 MiB
			Version:      "dev",
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
// os.LookupEnv, test stubs, and pre-parsed .env file maps.
// Adding a new config field requires only one change here.
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
	// clearable sets dest to the trimmed value even when empty.
	// Use this for fields where an empty string is a meaningful override — for
	// example APP_WRITE_KEY="" explicitly disables the write guard even when the
	// .env file set a key. The str helper silently skips empty values, which
	// would make it impossible to clear a key that was set in .env.
	clearable := func(key string, dest *string) {
		if val, ok := lookupEnv(key); ok {
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
	clearable("APP_WRITE_KEY", &cfg.App.WriteKey)
	boolf("APP_TLS_ENABLED", &cfg.Core.TLS.Enabled)
	str("APP_TLS_CERT_FILE", &cfg.Core.TLS.CertFile)
	str("APP_TLS_KEY_FILE", &cfg.Core.TLS.KeyFile)
	// APP_CORS_ALLOWED_ORIGINS: comma-separated list of allowed origins.
	// Example: "https://app.example.com,https://admin.example.com"
	// Empty or absent keeps the default permissive ["*"] behaviour.
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

// applyEnvMap applies a pre-parsed key-value map (e.g., from a .env file) to cfg.
// It adapts the map to a lookupEnv signature and delegates to applyEnv, so the
// field mapping is defined in exactly one place.
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
	fs := flag.NewFlagSet("standard-service", flag.ContinueOnError)
	fs.StringVar(&cfg.Core.Addr, "addr", cfg.Core.Addr, "listen address")
	fs.StringVar(&cfg.App.EnvFile, "env-file", cfg.App.EnvFile, "path to .env file")
	if len(args) == 0 {
		return fs.Parse(nil)
	}
	// Collect registered flag names from the FlagSet itself so filterFlagArgs
	// stays in sync automatically when new flags are added above.
	// Adding a new flag to fs only requires the fs.StringVar call above;
	// no separate name list needs to be maintained.
	known := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) { known[f.Name] = true })
	return fs.Parse(filterFlagArgs(args[1:], known))
}

// filterFlagArgs extracts only the flag arguments known to this service from args,
// silently dropping unknown flags and positional arguments. This lets the process
// start cleanly even when a container runtime, process supervisor, or test harness
// injects extra arguments that the flag.FlagSet has no definition for. Without
// filtering, flag.ContinueOnError would still return an error on the first unknown
// flag, which would prevent startup rather than tolerating environment noise.
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
