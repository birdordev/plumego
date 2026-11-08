# x/resilience

## Purpose

`x/resilience` contains reusable resilience components that are not part of the stable core and do not belong to a single feature family.

## v1 Status

- `experimental` in the Plumego v1 support matrix
- Included in repository release scope, but compatibility is not frozen

## Use this module when

- the task is a reusable circuit breaker or similar resilience primitive
- the task is a reusable rate limiter or keyed limiter primitive
- the behavior should be shared across multiple extension families

## Do not use this module for

- app bootstrap
- stable security policy
- feature-specific orchestration that belongs in `x/ai`, `x/gateway`, or another owning extension
- AI provider fallback, provider request keying, or AI error classification

## First files to read

- `x/resilience/module.yaml`
- `x/resilience/circuitbreaker`
- `specs/extension-taxonomy.yaml`

## Public entrypoints

- `circuitbreaker.New`
- `circuitbreaker.Middleware` / `circuitbreaker.MiddlewareWithErrorHandler`
- `ratelimit.New`
- `ratelimit.NewKeyed`

---

## Circuit Breaker

`circuitbreaker.New` creates a three-state breaker (Closed → Open → Half-Open):

```go
import "github.com/birdordev/plumego/x/resilience/circuitbreaker"

breaker := circuitbreaker.New(circuitbreaker.Config{
    FailureThreshold:  5,               // open after 5 consecutive failures
    SuccessThreshold:  2,               // close again after 2 successes in half-open
    Timeout:           10 * time.Second, // stay open for this long before half-open
    OnStateChange: func(from, to circuitbreaker.State) {
        log.Printf("circuit breaker: %s → %s", from, to)
    },
})

// Wrap any operation.
err := breaker.Call(func() error {
    return callDownstreamService()
})
if err != nil {
    // err may be circuitbreaker.ErrOpen if the breaker is open.
}

// Context-aware variant.
err = breaker.CallWithContext(ctx, func() error {
    return callDownstreamService()
})

// Inspect state and counts.
state := breaker.State()   // circuitbreaker.StateClosed, StateOpen, StateHalfOpen
counts := breaker.Counts() // requests, failures, successes
```

### HTTP Middleware

Wraps a handler; 5xx responses count as failures:

```go
import (
    "github.com/birdordev/plumego/x/resilience/circuitbreaker"
)

breaker := circuitbreaker.New(circuitbreaker.Config{FailureThreshold: 3})

// Mount on a route or route group.
app.Get("/downstream", circuitbreaker.Middleware(circuitbreaker.MiddlewareConfig{
    Breaker: breaker,
})(downstreamHandler))
```

When the breaker is open, the middleware returns HTTP 503 with a structured
`contract.APIError`. When the concurrent request limit is exceeded in half-open
state, it returns HTTP 429.

---

## Rate Limiter

`ratelimit.New` creates a single token-bucket limiter. `ratelimit.NewKeyed`
manages per-key buckets (e.g., per IP or per tenant ID):

```go
import "github.com/birdordev/plumego/x/resilience/ratelimit"

// Single bucket: 100 tokens/second, burst of 20.
limiter := ratelimit.New(100, 20)

// Non-blocking check.
if !limiter.Allow() {
    // rate limit exceeded
}

// Blocking wait until a token is available (or context cancelled).
if err := limiter.Wait(ctx); err != nil {
    // context cancelled or deadline exceeded
}

// Dynamic reconfiguration.
limiter.UpdateRate(200, 40)
```

### Per-Key Buckets

```go
// Per-client rate limiting keyed by IP address.
keyedLimiter := ratelimit.NewKeyed(50, 10) // 50 req/s per key, burst 10

func rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        clientIP := r.RemoteAddr
        if !keyedLimiter.Allow(clientIP) {
            w.Header().Set("Retry-After", "1")
            http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}

// Clean up buckets for inactive clients.
keyedLimiter.Delete("192.168.1.1")
```

---

## Main risks when changing this module

- breaker threshold regression
- keyed limiter behavior drift
- adapter behavior regression
- hidden shared state between callers

## Canonical change shape

- keep reusable resilience primitives here instead of in stable roots
- keep HTTP or transport adapters explicit and colocated with the primitive when they are generic
- keep feature-specific orchestration in the owning extension package
- keep AI-provider wrappers in `x/ai/resilience`

## Boundary rules

- `x/resilience` owns reusable circuit breaker and rate limit primitives; do not add these to stable `security` or stable `middleware`
- keep resilience primitive state instance-scoped; do not introduce package-level global state or implicit registration
- keep HTTP or transport adapters local to the owning extension when they are generic; do not push them into stable roots
- feature-specific orchestration (retry strategies tied to business rules) belongs in the owning extension, not in `x/resilience`
- `x/ai/circuitbreaker` and `x/ai/ratelimit` remain AI compatibility surfaces; do not move their exported symbols here without a dedicated symbol-change card

## Validation commands

- `go test -race -timeout 60s ./x/resilience/...`
- `go test -timeout 20s ./x/resilience/...`
- `go vet ./x/resilience/...`

For the detailed AI boundary decision, see
`docs/concepts/x-ai-resilience-boundary.md`.
