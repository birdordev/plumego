// Package resilience provides resilience wrappers for AI providers.
//
// This package combines rate limiting and circuit breaking to create
// resilient providers that can handle failures gracefully.
package resilience

import (
	"context"
	"errors"
	"fmt"

	"github.com/birdordev/plumego/x/ai/provider"
	sharedcircuitbreaker "github.com/birdordev/plumego/x/resilience/circuitbreaker"
	sharedratelimit "github.com/birdordev/plumego/x/resilience/ratelimit"
)

var (
	// ErrNilProvider is returned when a resilient provider is built without an
	// underlying provider.
	ErrNilProvider = errors.New("ai resilience: provider cannot be nil")

	// ErrNilRequest is returned when a provider request is nil.
	ErrNilRequest = errors.New("ai resilience: completion request cannot be nil")

	// ErrRateLimitExceeded is returned when the rate limit is exceeded.
	ErrRateLimitExceeded = errors.New("ai resilience: rate limit exceeded")
)

// ResilientProvider wraps a provider with rate limiting and circuit breaking.
type ResilientProvider struct {
	provider       provider.Provider
	rateLimiter    *sharedratelimit.KeyedBuckets
	circuitBreaker *sharedcircuitbreaker.CircuitBreaker
	name           string
}

// Config configures a resilient provider.
type Config struct {
	Provider       provider.Provider
	RateLimiter    *sharedratelimit.KeyedBuckets
	CircuitBreaker *sharedcircuitbreaker.CircuitBreaker
}

// NewResilientProvider creates a new resilient provider and returns validation
// errors for invalid composition.
func NewResilientProvider(config Config) (*ResilientProvider, error) {
	if config.Provider == nil {
		return nil, ErrNilProvider
	}

	name := config.Provider.Name()
	if config.CircuitBreaker != nil {
		name = config.CircuitBreaker.Name()
	}

	return &ResilientProvider{
		provider:       config.Provider,
		rateLimiter:    config.RateLimiter,
		circuitBreaker: config.CircuitBreaker,
		name:           name,
	}, nil
}

// Name implements provider.Provider
func (rp *ResilientProvider) Name() string {
	if rp == nil || rp.provider == nil {
		return ""
	}
	return rp.provider.Name()
}

// Complete implements provider.Provider with rate limiting and circuit breaking.
func (rp *ResilientProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	if err := rp.validateRequest(req); err != nil {
		return nil, err
	}

	if rp.rateLimiter != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !rp.rateLimiter.Allow(rp.getRateLimitKey(req)) {
			return nil, ErrRateLimitExceeded
		}
	}

	if rp.circuitBreaker != nil {
		var resp *provider.CompletionResponse
		var err error

		breakerErr := rp.circuitBreaker.CallWithContext(ctx, func() error {
			resp, err = rp.provider.Complete(ctx, req)
			return err
		})
		if breakerErr != nil {
			return nil, breakerErr
		}
		return resp, err
	}

	return rp.provider.Complete(ctx, req)
}

// CompleteStream implements provider.Provider with rate limiting and circuit breaking.
func (rp *ResilientProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	if err := rp.validateRequest(req); err != nil {
		return nil, err
	}

	if rp.rateLimiter != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !rp.rateLimiter.Allow(rp.getRateLimitKey(req)) {
			return nil, ErrRateLimitExceeded
		}
	}

	if rp.circuitBreaker != nil {
		var reader *provider.StreamReader
		var err error

		breakerErr := rp.circuitBreaker.CallWithContext(ctx, func() error {
			reader, err = rp.provider.CompleteStream(ctx, req)
			return err
		})
		if breakerErr != nil {
			return nil, breakerErr
		}
		return reader, err
	}

	return rp.provider.CompleteStream(ctx, req)
}

// ListModels implements provider.Provider.
func (rp *ResilientProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	if rp == nil || rp.provider == nil {
		return nil, ErrNilProvider
	}

	if rp.circuitBreaker != nil {
		var models []provider.Model
		var err error

		breakerErr := rp.circuitBreaker.CallWithContext(ctx, func() error {
			models, err = rp.provider.ListModels(ctx)
			return err
		})
		if breakerErr != nil {
			return nil, breakerErr
		}
		return models, err
	}

	return rp.provider.ListModels(ctx)
}

// GetModel implements provider.Provider.
func (rp *ResilientProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	if rp == nil || rp.provider == nil {
		return nil, ErrNilProvider
	}

	if rp.circuitBreaker != nil {
		var model *provider.Model
		var err error

		breakerErr := rp.circuitBreaker.CallWithContext(ctx, func() error {
			model, err = rp.provider.GetModel(ctx, modelID)
			return err
		})
		if breakerErr != nil {
			return nil, breakerErr
		}
		return model, err
	}

	return rp.provider.GetModel(ctx, modelID)
}

// CountTokens implements provider.Provider.
func (rp *ResilientProvider) CountTokens(text string) (int, error) {
	if rp == nil || rp.provider == nil {
		return 0, ErrNilProvider
	}
	return rp.provider.CountTokens(text)
}

func (rp *ResilientProvider) validateRequest(req *provider.CompletionRequest) error {
	if rp == nil || rp.provider == nil {
		return ErrNilProvider
	}
	if req == nil {
		return ErrNilRequest
	}
	return nil
}

func (rp *ResilientProvider) getRateLimitKey(req *provider.CompletionRequest) string {
	return fmt.Sprintf("%s:%s", rp.provider.Name(), req.Model)
}

// CircuitBreakerState returns the current circuit breaker state.
func (rp *ResilientProvider) CircuitBreakerState() sharedcircuitbreaker.State {
	if rp.circuitBreaker != nil {
		return rp.circuitBreaker.State()
	}
	return sharedcircuitbreaker.StateClosed
}

// CircuitBreakerStats returns circuit breaker statistics.
func (rp *ResilientProvider) CircuitBreakerStats() sharedcircuitbreaker.Stats {
	if rp.circuitBreaker != nil {
		return rp.circuitBreaker.Stats()
	}
	return sharedcircuitbreaker.Stats{
		Name:  rp.name,
		State: sharedcircuitbreaker.StateClosed,
	}
}

// RateLimitRemaining returns remaining rate limit tokens for the given model.
func (rp *ResilientProvider) RateLimitRemaining(ctx context.Context, model string) (int, error) {
	if rp == nil || rp.provider == nil {
		return 0, ErrNilProvider
	}
	if rp.rateLimiter != nil {
		key := fmt.Sprintf("%s:%s", rp.provider.Name(), model)
		return rp.rateLimiter.Remaining(key), nil
	}
	return -1, nil
}
