package gateway

import (
	"github.com/birdordev/plumego/x/resilience/circuitbreaker"
)

// CircuitBreaker wraps the circuit breaker for a backend
type CircuitBreaker interface {
	Call(fn func() error) error
	State() circuitbreaker.State
	Stats() circuitbreaker.Stats
	Reset()
	Trip()
}

// newBackendCircuitBreaker creates a circuit breaker for a backend
func newBackendCircuitBreaker(backendURL string, config *CircuitBreakerConfig) CircuitBreaker {
	if config == nil {
		config = &CircuitBreakerConfig{
			FailureThreshold: 0.5,
			SuccessThreshold: 3,
			MinRequests:      10,
		}
	}

	cbConfig := circuitbreaker.Config{
		Name:             backendURL,
		FailureThreshold: config.FailureThreshold,
		SuccessThreshold: config.SuccessThreshold,
		Timeout:          config.Timeout,
		MinRequests:      config.MinRequests,
	}

	return circuitbreaker.New(cbConfig)
}
