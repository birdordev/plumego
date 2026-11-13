package kvstore

import (
	"errors"
	"math"
	"strings"
)

const (
	bytesPerMiB        = 1 << 20
	defaultMaxEntries  = 100000
	defaultMaxMemoryMB = 200
)

// Config configures the stable embedded KV primitive.
type Config struct {
	// DataDir is the explicit directory where the state file is stored.
	DataDir     string `json:"data_dir"`
	MaxEntries  int    `json:"max_entries"`
	MaxMemoryMB int    `json:"max_memory_mb"`
}

// Options is an alias for Config for backward compatibility.
//
// Deprecated: Use Config instead.
type Options = Config

// DefaultConfig returns the default configuration for the caller-selected
// data directory. DataDir remains explicit so callers choose where filesystem
// state is created.
func DefaultConfig(dataDir string) Config {
	cfg := Config{DataDir: dataDir}
	setConfigDefaults(&cfg)
	return cfg
}

// DefaultOptions returns the default stable KV options for the caller-selected
// data directory.
//
// Deprecated: Use DefaultConfig instead.
func DefaultOptions(dataDir string) Options {
	return DefaultConfig(dataDir)
}

func setConfigDefaults(cfg *Config) {
	cfg.DataDir = strings.TrimSpace(cfg.DataDir)
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = defaultMaxEntries
	}
	if cfg.MaxMemoryMB == 0 {
		cfg.MaxMemoryMB = defaultMaxMemoryMB
	}
}

func validateConfig(cfg Config) error {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return errors.New("kv: data dir is required")
	}
	if cfg.MaxEntries <= 0 {
		return errors.New("kv: max entries must be positive")
	}
	if cfg.MaxMemoryMB <= 0 {
		return errors.New("kv: max memory must be positive")
	}
	if int64(cfg.MaxMemoryMB) > math.MaxInt64/bytesPerMiB {
		return errors.New("kv: max memory exceeds representable byte capacity")
	}
	return nil
}
