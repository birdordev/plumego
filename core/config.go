package core

import (
	"fmt"
	"strings"
	"time"
)

const defaultDrainInterval = 500 * time.Millisecond

// TLSConfig controls core's basic TLS certificate loading.
type TLSConfig struct {
	Enabled  bool   // Whether Prepare should load certificate material into the prepared server
	CertFile string // Path to the PEM-encoded TLS certificate file
	KeyFile  string // Path to the PEM-encoded TLS private key file
}

// RouterConfig defines owned router behavior policy.
type RouterConfig struct {
	MethodNotAllowed bool // Whether method mismatches return 405 with an Allow header
}

// AppConfig defines the core-owned HTTP server and router policy.
type AppConfig struct {
	Addr   string       // Address copied into the prepared http.Server
	TLS    TLSConfig    // Basic TLS certificate loading policy
	Router RouterConfig // Router behavior policy
	// HTTP server hardening
	ReadTimeout       time.Duration // Maximum duration for reading the entire request, including the body
	ReadHeaderTimeout time.Duration // Maximum duration for reading request headers; zero uses ReadTimeout
	WriteTimeout      time.Duration // Maximum duration before timing out response writes
	IdleTimeout       time.Duration // Maximum time to wait for the next keep-alive request; zero uses ReadTimeout
	MaxHeaderBytes    int           // Maximum request header size; zero keeps the standard library default
	HTTP2Enabled      bool          // Whether prepared TLS servers keep automatic HTTP/2 support enabled
	DrainInterval     time.Duration // How often to log open connection counts while draining; non-positive values use the default
}

// DefaultConfig returns the canonical baseline application configuration.
func DefaultConfig() AppConfig {
	return AppConfig{
		Addr:              ":8080",
		TLS:               TLSConfig{Enabled: false},
		Router:            RouterConfig{MethodNotAllowed: false},
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
		HTTP2Enabled:      true,
		DrainInterval:     defaultDrainInterval,
	}
}

func validateServerConfig(cfg AppConfig) error {
	if strings.TrimSpace(cfg.Addr) == "" {
		return fmt.Errorf("server address cannot be empty")
	}
	if cfg.ReadTimeout < 0 {
		return fmt.Errorf("read timeout cannot be negative")
	}
	if cfg.ReadHeaderTimeout < 0 {
		return fmt.Errorf("read header timeout cannot be negative")
	}
	if cfg.WriteTimeout < 0 {
		return fmt.Errorf("write timeout cannot be negative")
	}
	if cfg.IdleTimeout < 0 {
		return fmt.Errorf("idle timeout cannot be negative")
	}
	if cfg.MaxHeaderBytes < 0 {
		return fmt.Errorf("max header bytes cannot be negative")
	}
	return nil
}

// PreparationState describes the app's canonical kernel preparation phase.
type PreparationState string

const (
	PreparationStateMutable         PreparationState = "mutable"
	PreparationStateHandlerPrepared PreparationState = "handler_prepared"
	PreparationStateServerPrepared  PreparationState = "server_prepared"
)
