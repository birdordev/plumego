package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
)

// Config holds the proxy configuration
type Config struct {
	// Targets is a static list of backend URLs
	// Either Targets or Discovery must be provided
	Targets []string

	// ServiceName is the name of the service for dynamic discovery
	// Used with Discovery
	ServiceName string

	// Discovery is the service discovery interface
	// Either Targets or Discovery must be provided
	Discovery ServiceDiscovery

	// LoadBalancer is the load balancing strategy
	// Default: RoundRobinBalancer
	LoadBalancer LoadBalancer

	// Transport is the HTTP transport configuration
	// Default: DefaultTransportConfig()
	Transport *TransportConfig

	// PathRewrite is a function to rewrite request paths
	// Example: StripPrefix("/api")
	PathRewrite PathRewriteFunc

	// ModifyRequest is a hook to modify the outgoing request
	// Called after path rewriting
	ModifyRequest RequestModifier

	// ModifyResponse is a hook to modify the incoming response
	// Called before returning response to client
	ModifyResponse ResponseModifier

	// ErrorHandler handles proxy errors
	// Default: defaultErrorHandler
	ErrorHandler ErrorHandlerFunc

	// HealthCheck configures active health checking
	// Default: nil (disabled)
	HealthCheck *HealthCheckConfig

	// FailureThreshold is the number of consecutive failures
	// before marking a backend as unhealthy
	// Default: 3
	FailureThreshold uint32

	// Timeout is the proxy request timeout
	// Default: 30 seconds
	Timeout time.Duration

	// RetryCount is the number of retries on failure
	// Default: 2
	RetryCount int

	// RetryBackoff is the base duration for retry backoff
	// Default: 100ms
	RetryBackoff time.Duration

	// WebSocketEnabled enables WebSocket proxying
	// Default: false
	WebSocketEnabled bool

	// PreserveHost preserves the original Host header
	// Default: false (sets Host to backend URL)
	PreserveHost bool

	// AddForwardedHeaders adds X-Forwarded-* headers
	// Default: true
	AddForwardedHeaders bool

	// RemoveHopByHop removes hop-by-hop headers
	// Default: true
	RemoveHopByHop bool

	// BufferPool is a pool of buffers for copying request/response bodies
	// Default: nil (creates new buffers)
	BufferPool BufferPool

	// CircuitBreakerEnabled enables circuit breaker per backend
	// Default: false
	CircuitBreakerEnabled bool

	// CircuitBreakerConfig configures the circuit breaker
	// Only used if CircuitBreakerEnabled is true
	CircuitBreakerConfig *CircuitBreakerConfig
}

// Validate validates the proxy configuration
func (c *Config) Validate() error {
	// Must have either static targets or service discovery
	if len(c.Targets) == 0 && c.Discovery == nil {
		return ErrInvalidConfig
	}

	// Cannot have both targets and discovery
	if len(c.Targets) > 0 && c.Discovery != nil {
		return ErrInvalidConfig
	}

	// If using discovery, must have service name
	if c.Discovery != nil && c.ServiceName == "" {
		return ErrInvalidConfig
	}

	return nil
}

// WithDefaults returns a new Config with default values applied
func (c *Config) WithDefaults() *Config {
	config := *c

	// Set default load balancer
	if config.LoadBalancer == nil {
		config.LoadBalancer = NewRoundRobinBalancer()
	}

	// Set default transport config
	if config.Transport == nil {
		config.Transport = DefaultTransportConfig()
	}

	// Set default error handler
	if config.ErrorHandler == nil {
		config.ErrorHandler = defaultErrorHandler
	}

	// Set default failure threshold
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 3
	}

	// Set default timeout
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	// Set default retry count
	if config.RetryCount == 0 {
		config.RetryCount = 2
	}

	// Set default retry backoff
	if config.RetryBackoff == 0 {
		config.RetryBackoff = 100 * time.Millisecond
	}

	// Enable forwarded headers by default
	if !config.AddForwardedHeaders {
		config.AddForwardedHeaders = true
	}

	// Enable hop-by-hop removal by default
	if !config.RemoveHopByHop {
		config.RemoveHopByHop = true
	}

	return &config
}

// ErrorHandlerFunc is a function that handles proxy errors
type ErrorHandlerFunc func(w http.ResponseWriter, r *http.Request, err error)

// defaultErrorHandler is the default error handler
func defaultErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNoBackends) || errors.Is(err, ErrNoHealthyBackends):
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeUnavailable).
			Message("service unavailable").
			Build())
	case errors.Is(err, ErrBackendTimeout):
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeTimeout).
			Message("gateway timeout").
			Build())
	default:
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadGateway).
			Message("bad gateway").
			Build())
	}
}

// HealthCheckConfig configures active health checking
type HealthCheckConfig struct {
	// Interval is the time between health checks
	// Default: 10 seconds
	Interval time.Duration

	// Timeout is the health check timeout
	// Default: 5 seconds
	Timeout time.Duration

	// Path is the health check endpoint path
	// Default: /health
	Path string

	// Method is the HTTP method for health checks
	// Default: GET
	Method string

	// ExpectedStatus is the expected HTTP status code
	// Default: 200
	ExpectedStatus int

	// OnHealthChange is called when backend health changes
	OnHealthChange func(backend *Backend, healthy bool)
}

// WithDefaults returns a HealthCheckConfig with default values
func (h *HealthCheckConfig) WithDefaults() *HealthCheckConfig {
	config := *h

	if config.Interval == 0 {
		config.Interval = 10 * time.Second
	}

	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}

	if config.Path == "" {
		config.Path = "/health"
	}

	if config.Method == "" {
		config.Method = http.MethodGet
	}

	if config.ExpectedStatus == 0 {
		config.ExpectedStatus = http.StatusOK
	}

	return &config
}

// ServiceDiscovery defines the interface for service discovery
type ServiceDiscovery interface {
	// Resolve returns the list of backend URLs for a service
	Resolve(ctx context.Context, serviceName string) ([]string, error)

	// Watch returns a channel that receives backend updates
	// The channel is closed when the context is cancelled
	Watch(ctx context.Context, serviceName string) (<-chan []string, error)
}

// BufferPool is a pool of byte buffers
type BufferPool interface {
	Get() []byte
	Put([]byte)
}

// CircuitBreakerConfig configures circuit breaker behavior
type CircuitBreakerConfig struct {
	// FailureThreshold is the failure rate threshold (0.0-1.0)
	// Default: 0.5 (50%)
	FailureThreshold float64

	// SuccessThreshold is the number of successes needed to close circuit
	// Default: 3
	SuccessThreshold uint64

	// Timeout is how long circuit stays open before trying half-open
	// Default: 30 seconds
	Timeout time.Duration

	// MinRequests is minimum requests before evaluating failure rate
	// Default: 10
	MinRequests uint64
}
