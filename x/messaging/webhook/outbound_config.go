package webhook

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrNilValueReader reports a missing configuration value reader.
var ErrNilValueReader = errors.New("webhookout: config reader is required")

// DropPolicy defines the queue overflow behavior.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	config := webhookout.Config{
//		DropPolicy: webhookout.BlockWithLimit,
//		BlockWait:  100 * time.Millisecond,
//	}
type DropPolicy string

const (
	// DropNewest drops the newest task when queue is full.
	// Use this when you want to prioritize older tasks.
	DropNewest DropPolicy = "drop_newest"

	// BlockWithLimit blocks on enqueue with timeout when queue is full.
	// Use this when you want to wait for queue space.
	BlockWithLimit DropPolicy = "block_timeout"

	// FailFast immediately returns error when queue is full.
	// Use this when you want to fail fast and handle errors upstream.
	FailFast DropPolicy = "fail_fast"
)

// Config holds webhook delivery service configuration.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	config := webhookout.Config{
//		Enabled:             true,
//		QueueSize:           4096,
//		Workers:             16,
//		DrainMax:            10 * time.Second,
//		DropPolicy:          webhookout.BlockWithLimit,
//		BlockWait:           100 * time.Millisecond,
//		DefaultTimeout:      10 * time.Second,
//		DefaultMaxRetries:   8,
//		BackoffBase:         1 * time.Second,
//		BackoffMax:          60 * time.Second,
//		RetryOn429:          true,
//		AllowPrivateNetwork: false,
//	}
type Config struct {
	// Enabled determines if the webhook service is active
	Enabled bool

	// QueueSize is the size of the delivery queue
	QueueSize int

	// Workers is the number of worker goroutines
	Workers int

	// DrainMax is the maximum time to wait for queue drain on shutdown
	DrainMax time.Duration

	// DropPolicy defines behavior when queue is full
	DropPolicy DropPolicy

	// BlockWait is the maximum time to block when queue is full
	BlockWait time.Duration

	// DefaultTimeout is the default HTTP request timeout
	DefaultTimeout time.Duration

	// DefaultMaxRetries is the default maximum number of retry attempts
	DefaultMaxRetries int

	// BackoffBase is the base delay for exponential backoff
	BackoffBase time.Duration

	// BackoffMax is the maximum delay for exponential backoff
	BackoffMax time.Duration

	// RetryOn429 determines if 429 (Too Many Requests) responses should be retried
	RetryOn429 bool

	// AllowPrivateNetwork determines if webhooks can be sent to private network addresses
	AllowPrivateNetwork bool
}

// DefaultConfig returns production-ready defaults.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	config := webhookout.DefaultConfig()
func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		QueueSize:           2048,
		Workers:             8,
		DrainMax:            5 * time.Second,
		DropPolicy:          BlockWithLimit,
		BlockWait:           50 * time.Millisecond,
		DefaultTimeout:      5 * time.Second,
		DefaultMaxRetries:   6,
		BackoffBase:         500 * time.Millisecond,
		BackoffMax:          30 * time.Second,
		RetryOn429:          true,
		AllowPrivateNetwork: false,
	}
}

// ValueReader provides the configuration values required to construct Config.
type ValueReader interface {
	GetBool(key string, defaultValue bool) bool
	GetInt(key string, defaultValue int) int
	GetDurationMs(key string, defaultValueMs int) time.Duration
	GetString(key, defaultValue string) string
}

// ConfigFromReader creates config from an explicit configuration reader and
// returns an error for invalid inputs.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	config, err := webhookout.ConfigFromReader(reader)
func ConfigFromReader(reader ValueReader) (Config, error) {
	if reader == nil {
		return Config{}, ErrNilValueReader
	}

	cfg := Config{
		Enabled:    reader.GetBool("WEBHOOK_ENABLED", true),
		QueueSize:  reader.GetInt("WEBHOOK_QUEUE_SIZE", 2048),
		Workers:    reader.GetInt("WEBHOOK_WORKERS", 8),
		DrainMax:   reader.GetDurationMs("WEBHOOK_DRAIN_MAX_MS", 5000),
		DropPolicy: DropPolicy(reader.GetString("WEBHOOK_DROP_POLICY", string(BlockWithLimit))),
		BlockWait:  reader.GetDurationMs("WEBHOOK_BLOCK_WAIT_MS", 50),

		DefaultTimeout:    reader.GetDurationMs("WEBHOOK_DEFAULT_TIMEOUT_MS", 5000),
		DefaultMaxRetries: reader.GetInt("WEBHOOK_DEFAULT_MAX_RETRIES", 6),
		BackoffBase:       reader.GetDurationMs("WEBHOOK_BACKOFF_BASE_MS", 500),
		BackoffMax:        reader.GetDurationMs("WEBHOOK_BACKOFF_MAX_MS", 30000),
		RetryOn429:        reader.GetBool("WEBHOOK_RETRY_ON_429", true),

		AllowPrivateNetwork: reader.GetBool("WEBHOOK_ALLOW_PRIVATE_NET", false),
	}

	// Validate and apply defaults
	if cfg.QueueSize < 1 {
		cfg.QueueSize = 1
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.BlockWait < 0 {
		cfg.BlockWait = 0
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 500 * time.Millisecond
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 30 * time.Second
	}
	if cfg.DrainMax <= 0 {
		cfg.DrainMax = 5 * time.Second
	}

	// Validate drop policy
	switch cfg.DropPolicy {
	case DropNewest, BlockWithLimit, FailFast:
	default:
		cfg.DropPolicy = BlockWithLimit
	}

	return cfg, nil
}

// ConfigFromEnv creates config from process environment variables.
func ConfigFromEnv() Config {
	cfg, _ := ConfigFromReader(envReader{})
	return cfg
}

type envReader struct{}

func (envReader) GetBool(key string, defaultValue bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch value {
	case "1", "true", "yes", "y", "on", "t":
		return true
	case "0", "false", "no", "n", "off", "f":
		return false
	case "":
		return defaultValue
	default:
		return defaultValue
	}
}

func (envReader) GetInt(key string, defaultValue int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return defaultValue
}

func (envReader) GetDurationMs(key string, defaultValueMs int) time.Duration {
	return time.Duration(envReader{}.GetInt(key, defaultValueMs)) * time.Millisecond
}

func (envReader) GetString(key, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	return value
}

// Validate checks if configuration is valid.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	config := webhookout.DefaultConfig()
//	if err := config.Validate(); err != nil {
//		// Handle invalid configuration
//	}
func (c Config) Validate() error {
	if c.QueueSize < 1 {
		return errors.New("queue_size must be at least 1")
	}
	if c.Workers < 1 {
		return errors.New("workers must be at least 1")
	}
	if c.DefaultTimeout <= 0 {
		return errors.New("default_timeout must be positive")
	}
	if c.DefaultMaxRetries < 0 {
		return errors.New("default_max_retries cannot be negative")
	}
	if c.BackoffBase <= 0 {
		return errors.New("backoff_base must be positive")
	}
	if c.BackoffMax <= 0 {
		return errors.New("backoff_max must be positive")
	}
	if c.BackoffBase > c.BackoffMax {
		return errors.New("backoff_base cannot be greater than backoff_max")
	}
	return nil
}
