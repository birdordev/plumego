// Package circuitbreaker provides circuit breaker pattern implementation
//
// The circuit breaker prevents cascading failures by detecting when a service
// is failing and temporarily blocking requests to give it time to recover.
//
// States:
//   - Closed: Normal operation, requests pass through
//   - Open: Service is failing, requests fail fast
//   - Half-Open: Testing if service recovered, limited requests allowed
//
// State transitions:
//   - Closed -> Open: Failure rate exceeds threshold
//   - Open -> Half-Open: After timeout period
//   - Half-Open -> Closed: Success rate meets threshold
//   - Half-Open -> Open: Failures continue
//
// Example:
//
//	cb := circuitbreaker.New(circuitbreaker.Config{
//		Name:             "user-service",
//		FailureThreshold: 0.5,  // Open after 50% failures
//		SuccessThreshold: 3,    // Need 3 successes to close
//		Timeout:          30 * time.Second,
//		MinRequests:      10,   // Need 10 requests before evaluating
//	})
//
//	err := cb.Call(func() error {
//		return callBackend()
//	})
package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// State represents the circuit breaker state
type State int32

const (
	// StateClosed means requests are passing through normally
	StateClosed State = iota

	// StateOpen means the circuit is open and requests fail fast
	StateOpen

	// StateHalfOpen means the circuit is testing if the service recovered
	StateHalfOpen
)

// String returns the state name
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name   string
	config Config

	// stateMu protects transitions, admitted probes, and their statistics.
	stateMu      sync.RWMutex
	state        State
	stateChanged time.Time
	generation   uint64
	inFlight     uint64
	counts       Counts

	// Hooks
	onStateChange func(from, to State)

	// Testing
	clock Clock
}

// Config holds circuit breaker configuration
type Config struct {
	// Name is the circuit breaker name (for logging/metrics)
	Name string

	// FailureThreshold is the failure rate threshold (0.0-1.0)
	// When exceeded, the circuit opens
	// Default: 0.5 (50%)
	FailureThreshold float64

	// SuccessThreshold is the number of consecutive successes
	// needed in half-open state to close the circuit
	// Default: 3
	SuccessThreshold uint64

	// Timeout is how long the circuit stays open before
	// transitioning to half-open
	// Default: 30 seconds
	Timeout time.Duration

	// MinRequests is the minimum number of requests needed
	// before calculating failure rate
	// Default: 10
	MinRequests uint64

	// MaxRequests is the maximum number of concurrent requests
	// allowed in half-open state
	// Default: 1
	MaxRequests uint64

	// OnStateChange is called when the state changes
	OnStateChange func(from, to State)

	// ShouldTrip is a custom function to determine if circuit should open
	// If nil, uses default failure rate calculation
	ShouldTrip func(counts Counts) bool
}

// Counts holds circuit breaker metrics
type Counts struct {
	Requests  uint64
	Successes uint64
	Failures  uint64

	// Consecutive counts (for half-open state)
	ConsecutiveSuccesses uint64
	ConsecutiveFailures  uint64
}

// FailureRate returns the failure rate (0.0-1.0)
func (c Counts) FailureRate() float64 {
	if c.Requests == 0 {
		return 0.0
	}
	return float64(c.Failures) / float64(c.Requests)
}

// SuccessRate returns the success rate (0.0-1.0)
func (c Counts) SuccessRate() float64 {
	if c.Requests == 0 {
		return 0.0
	}
	return float64(c.Successes) / float64(c.Requests)
}

// New creates a new circuit breaker
func New(config Config) *CircuitBreaker {
	// Apply defaults
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 0.5
	}
	if config.SuccessThreshold == 0 {
		config.SuccessThreshold = 3
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MinRequests == 0 {
		config.MinRequests = 10
	}
	if config.MaxRequests == 0 {
		config.MaxRequests = 1
	}
	if config.Name == "" {
		config.Name = "circuit-breaker"
	}

	cb := &CircuitBreaker{
		name:          config.Name,
		config:        config,
		stateChanged:  time.Now(),
		onStateChange: config.OnStateChange,
		clock:         &realClock{},
	}

	return cb
}

// Call executes the function with circuit breaker protection
func (cb *CircuitBreaker) Call(fn func() error) (err error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		return err
	}
	completed := false
	defer func() { cb.afterRequest(generation, err, completed) }()
	err = fn()
	completed = true
	return err
}

// CallWithContext executes the function with context
func (cb *CircuitBreaker) CallWithContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return cb.Call(fn)
}

func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	var notify func()
	cb.stateMu.Lock()
	defer func() {
		cb.stateMu.Unlock()
		if notify != nil {
			notify()
		}
	}()

	if cb.state == StateOpen {
		if cb.clock.Since(cb.stateChanged) < cb.config.Timeout {
			return 0, ErrCircuitOpen
		}
		notify = cb.setStateLocked(StateHalfOpen)
	}
	if cb.state == StateHalfOpen {
		if cb.inFlight >= cb.config.MaxRequests {
			return 0, ErrTooManyRequests
		}
		cb.inFlight++
	}
	return cb.generation, nil
}

func (cb *CircuitBreaker) afterRequest(generation uint64, err error, completed bool) {
	var notify func()
	cb.stateMu.Lock()
	if generation != cb.generation {
		cb.stateMu.Unlock()
		return
	}

	if cb.state == StateHalfOpen {
		cb.inFlight--
		if completed {
			if err != nil {
				notify = cb.setStateLocked(StateOpen)
			} else {
				cb.counts.ConsecutiveSuccesses++
				if cb.counts.ConsecutiveSuccesses >= cb.config.SuccessThreshold {
					notify = cb.setStateLocked(StateClosed)
				}
			}
		}
		cb.stateMu.Unlock()
		if notify != nil {
			notify()
		}
		return
	}

	if !completed {
		cb.stateMu.Unlock()
		return
	}
	cb.counts.Requests++
	if err == nil {
		cb.counts.Successes++
		cb.stateMu.Unlock()
		return
	}
	cb.counts.Failures++
	counts := cb.counts
	if counts.Requests < cb.config.MinRequests {
		cb.stateMu.Unlock()
		return
	}
	if cb.config.ShouldTrip == nil {
		if counts.FailureRate() >= cb.config.FailureThreshold {
			notify = cb.setStateLocked(StateOpen)
		}
		cb.stateMu.Unlock()
	} else {
		cb.stateMu.Unlock()
		if cb.config.ShouldTrip(counts) {
			cb.stateMu.Lock()
			// The callback may have overlapped a newer state or changed it itself.
			if generation == cb.generation {
				notify = cb.setStateLocked(StateOpen)
			}
			cb.stateMu.Unlock()
		}
	}
	if notify != nil {
		notify()
	}
}

func (cb *CircuitBreaker) setState(newState State) {
	cb.stateMu.Lock()
	notify := cb.setStateLocked(newState)
	cb.stateMu.Unlock()
	if notify != nil {
		notify()
	}
}

// setStateLocked applies the transition and returns a notification for the caller
// to dispatch after unlocking.
func (cb *CircuitBreaker) setStateLocked(newState State) func() {
	oldState := cb.state
	if oldState == newState {
		return nil
	}

	cb.state = newState
	cb.stateChanged = cb.clock.Now()
	cb.generation++
	cb.inFlight = 0
	switch newState {
	case StateClosed:
		cb.counts.Requests = 0
		cb.counts.Failures = 0
		cb.counts.Successes = 0
	case StateHalfOpen:
		cb.counts.ConsecutiveSuccesses = 0
	}

	if fn := cb.onStateChange; fn != nil {
		return func() {
			go func() {
				defer func() { _ = recover() }()
				fn(oldState, newState)
			}()
		}
	}
	return nil
}

// State returns the current state
func (cb *CircuitBreaker) State() State {
	cb.stateMu.RLock()
	defer cb.stateMu.RUnlock()
	return cb.state
}

// Counts returns current metrics
func (cb *CircuitBreaker) Counts() Counts {
	cb.stateMu.RLock()
	defer cb.stateMu.RUnlock()
	return cb.counts
}

// Reset closes the circuit and clears its counts. Completions from calls
// admitted before Reset do not update the new counts.
func (cb *CircuitBreaker) Reset() {
	cb.stateMu.Lock()
	if cb.state == StateClosed {
		cb.generation++
	}
	notify := cb.setStateLocked(StateClosed)
	cb.counts = Counts{}
	cb.stateMu.Unlock()
	if notify != nil {
		notify()
	}
}

// Trip manually opens the circuit
func (cb *CircuitBreaker) Trip() {
	cb.setState(StateOpen)
}

// Name returns the circuit breaker name
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// Stats returns circuit breaker statistics
func (cb *CircuitBreaker) Stats() Stats {
	cb.stateMu.RLock()
	defer cb.stateMu.RUnlock()

	return Stats{
		Name:          cb.name,
		State:         cb.state,
		Counts:        cb.counts,
		StateChanged:  cb.stateChanged,
		StateDuration: cb.clock.Since(cb.stateChanged),
	}
}

// Stats holds circuit breaker statistics
type Stats struct {
	Name          string
	State         State
	Counts        Counts
	StateChanged  time.Time
	StateDuration time.Duration
}

// String returns a string representation
func (s Stats) String() string {
	return fmt.Sprintf("%s [%s] requests=%d successes=%d failures=%d rate=%.2f%%",
		s.Name, s.State, s.Counts.Requests, s.Counts.Successes, s.Counts.Failures,
		s.Counts.FailureRate()*100)
}

// Common errors
var (
	// ErrCircuitOpen is returned when the circuit is open
	ErrCircuitOpen = errors.New("circuit breaker is open")

	// ErrTooManyRequests is returned when too many requests in half-open state
	ErrTooManyRequests = errors.New("too many requests")
)

// Clock interface for time operations (allows testing)
type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type realClock struct{}

func (c *realClock) Now() time.Time {
	return time.Now()
}

func (c *realClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}
