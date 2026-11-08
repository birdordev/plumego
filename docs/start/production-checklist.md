# Production Checklist

A Plumego service ready for production should have these capabilities. This checklist maps each concern to the Plumego module and a reference implementation.

## Core Requirements

### ✅ Configuration Management

**What:** Load configuration from files, environment variables, or flags. Validate on startup.

**Why:** Services need different settings per environment (dev, staging, prod).

**Plumego module:** `internal/config/` (your code; see examples below)

**Reference:** 
- `reference/standard-service/internal/config/config.go` — Config struct with defaults and validation
- `examples/standard-api/internal/config/` — Copy this pattern

**Questions to answer:**
- How do I load config from a file?
- How do I override with environment variables?
- How do I validate required fields on startup?

### ✅ HTTP Server Lifecycle

**What:** Start, gracefully shutdown, handle signals.

**Why:** Services need predictable startup and shutdown. Graceful shutdown lets in-flight requests finish before exiting.

**Plumego module:** `core.App` with `Prepare()`, `Server()`, `Shutdown()`

**Reference:**
- `reference/standard-service/main.go` — Signal handling and graceful shutdown
- `examples/standard-api/main.go` — Complete example

**Pattern:**
```go
app.Prepare()  // Freeze routes
server := app.Server()
go server.ListenAndServe()

// Wait for SIGINT or SIGTERM
<-sigChan

// Graceful shutdown (30 second timeout)
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
server.Shutdown(ctx)
```

### ✅ Structured Logging

**What:** Log important events (startup, requests, errors) with context.

**Why:** Production debugging requires structured logs (JSON, key-value pairs) that can be aggregated and searched.

**Plumego module:** `log` (interface) + `middleware/accesslog` (HTTP logging)

**Reference:**
- `reference/standard-service/internal/app/app.go` — How to inject logger into app
- `middleware/accesslog/` — Log every HTTP request with status, duration, error

**Pattern:**
```go
logger := log.NewDefault()  // Or use your own implementation
app := core.New(cfg, core.AppDependencies{Logger: logger})
app.Use(accesslog.Handler(logger))  // Log HTTP requests
```

**Questions to answer:**
- Should I use JSON logging or text?
- How do I correlate logs from different services?
- What should I log (every request? errors only?)?

### ✅ Error Handling

**What:** Consistent error responses for all error types (validation, not found, internal server error).

**Why:** Clients need predictable error formats. Internal errors should not leak details.

**Plumego module:** `contract.WriteError()`

**Reference:**
- `examples/error-handling/main.go` — Shows validation, JSON binding, panic errors
- `docs/modules/contract/` — All error response helpers

**Pattern:**
```go
if err != nil {
    contract.WriteError(w, r, err)
    return
}
```

**Response (automatic):**
```json
{"error":"user not found"}
```

**Questions to answer:**
- How do I return custom error codes?
- How do I distinguish validation errors from internal errors?
- Should clients see stack traces?

### ✅ Health Checks

**What:** Endpoints that report service health and dependencies.

**Why:** Load balancers and orchestrators (Kubernetes, Docker, etc.) need to know if your service is ready.

**Plumego module:** `health` module

**Reference:**
- `reference/standard-service/internal/handler/health.go` — Health check implementation
- `docs/modules/health/` — Health check models

**Pattern:**
```go
// In routes
app.Get("/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    h := health.Status{
        Status: health.StatusHealthy,
    }
    contract.WriteResponse(w, r, http.StatusOK, h, nil)
}))

// For readiness (dependencies available)
app.Get("/readiness", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Check database, cache, etc.
    h := health.Status{
        Status: health.StatusHealthy,
        Components: []health.Component{
            {Name: "database", Status: health.StatusHealthy},
        },
    }
    contract.WriteResponse(w, r, http.StatusOK, h, nil)
}))
```

**Questions to answer:**
- What should /health return vs. /readiness?
- How often should I check dependencies?
- What status codes should I use?

### ✅ Graceful Recovery

**What:** Catch panics, log them, return 500 errors instead of crashing.

**Why:** A bad handler shouldn't crash the entire server. Panics should be logged for debugging.

**Plumego module:** `middleware/recovery`

**Reference:**
- `examples/error-handling/main.go` — Recovery middleware
- `docs/modules/middleware/` — All standard middleware

**Pattern:**
```go
app.Use(recovery.Handler())  // Add early in middleware chain
```

---

## Observability

### ✅ Metrics

**What:** Track key metrics: request count, latency, errors, custom business metrics.

**Why:** Understand service performance and health. Alert on anomalies.

**Plumego module:** `metrics` interface + `x/observability` for exporters (Prometheus, OTel)

**Reference:**
- `docs/modules/metrics/` — Metrics interfaces
- `docs/modules/x/observability/` — Exporter setup (Prometheus, OpenTelemetry)
- `reference/with-observability/` — Complete example with Prometheus

**Pattern (simple):**
```go
counter := metrics.NewCounter("http_requests_total")
counter.Increment(map[string]string{"method": "GET", "status": "200"})
```

**Pattern (with exporter):**
```go
import "github.com/birdordev/plumego/x/observability"

// Wire up Prometheus exporter
exporter := observability.NewPrometheusExporter()
defer exporter.Export()
```

**Questions to answer:**
- Which metrics are most important for my service?
- How do I export metrics to Prometheus or Datadog?
- How do I alert on metrics?

### ✅ Tracing (optional)

**What:** Track request flow across services and external calls.

**Why:** Multi-service debugging requires distributed tracing.

**Plumego module:** `middleware/tracing` + `x/observability`

**Reference:**
- `reference/with-observability/` — Complete example with OpenTelemetry
- `docs/modules/x/observability/` — Tracer setup

**When to add:** After basic observability is working.

---

## Security

### ✅ Request ID Carriage

**What:** Add unique ID to each request, include in logs and responses.

**Why:** Track a single request across services and logs.

**Plumego module:** `middleware/requestid`

**Reference:**
- `middleware/requestid/` — Middleware that adds request ID to context
- `reference/standard-service/internal/app/app.go` — How to use it

**Pattern:**
```go
app.Use(requestid.Handler())  // Adds to request context

// In handler:
reqID := contract.RequestID(r)
// Use in logs, responses, forward calls to other services
```

### ✅ Security Headers

**What:** Set HTTP security headers (Content-Security-Policy, X-Frame-Options, etc.).

**Why:** Prevent common web attacks (XSS, clickjacking, etc.).

**Plumego module:** `middleware/securityheaders` or `security` module

**Reference:**
- `middleware/securityheaders/` — Standard security headers
- `docs/modules/security/` — Auth and security patterns

**Pattern:**
```go
app.Use(securityheaders.Handler())  // Adds standard headers
```

### ✅ Input Validation

**What:** Validate all user input before processing.

**Why:** Invalid input can cause crashes, data corruption, or security issues.

**Plumego module:** `x/validate` (experimental) or custom validation

**Reference:**
- `examples/error-handling/main.go` — Manual validation patterns
- `docs/modules/x/validate/` — Validation bridge

**Pattern (manual, recommended):**
```go
if req.Email == "" {
    contract.WriteError(w, r, errors.New("email is required"))
    return
}
if len(req.Password) < 8 {
    contract.WriteError(w, r, errors.New("password must be at least 8 characters"))
    return
}
```

### ✅ Authentication (if needed)

**What:** Identify the user or service making the request.

**Why:** Control who can access your service.

**Plumego module:** `middleware/auth` + `security` module

**Reference:**
- `docs/modules/security/` — Auth adapters (JWT, API keys, OAuth, etc.)
- `reference/with-rest/` — Example with middleware-based auth
- `reference/with-tenant/` — Example with tenant resolution

**Pattern (JWT):**
```go
app.Use(auth.JWTHandler(publicKey))  // Validates JWT in Authorization header

// In handler:
user := contract.User(r)  // Claims from JWT
```

### ✅ Rate Limiting (if needed)

**What:** Limit request rate per user, IP, or API key.

**Why:** Prevent abuse, DoS attacks, and cost overruns.

**Plumego module:** `middleware/concurrencylimit`, `middleware/timeout`, or `x/resilience`

**Reference:**
- `middleware/concurrencylimit/` — Limit concurrent requests
- `docs/modules/x/resilience/` — Circuit breaker and rate limiter primitives
- `reference/with-rest/` — Example with rate limiting

**Pattern:**
```go
app.Use(concurrencylimit.Handler(100))  // Max 100 concurrent requests
```

---

## Deployment

### ✅ Graceful Shutdown

Already covered under "Core Requirements" (HTTP Server Lifecycle). Ensure your deployment platform (Kubernetes, Docker, etc.) gives the service time to shut down gracefully.

**Recommended:** 30-60 second shutdown timeout.

### ✅ Readiness Probe

Already covered under "Core Requirements" (Health Checks). Kubernetes and other orchestrators check `/readiness` before routing traffic.

### ✅ Zero-Downtime Deployments

**What:** Deploy new versions without losing requests in flight.

**Why:** Improve availability and user experience.

**How:** Combine graceful shutdown + readiness probes + load balancer health checks.

**Pattern:**
1. Load balancer stops routing requests to old instance
2. Old instance finishes in-flight requests (within timeout)
3. New instance starts and passes readiness check
4. Load balancer routes traffic to new instance

---

## Checklist for Your Service

Use this as a template. Copy and customize:

```markdown
## My Service Production Readiness

### Core
- [ ] Configuration loading and validation
- [ ] HTTP server with graceful shutdown
- [ ] Structured logging (JSON or key-value)
- [ ] Consistent error responses
- [ ] Health and readiness checks
- [ ] Panic recovery

### Observability
- [ ] Metrics (request count, latency, errors)
- [ ] Exporter (Prometheus, Datadog, etc.)
- [ ] Access logs
- [ ] Request ID carriage
- [ ] Tracing (optional, phase 2)

### Security
- [ ] Security headers
- [ ] Input validation
- [ ] Authentication (if needed)
- [ ] Rate limiting (if needed)
- [ ] TLS/HTTPS

### Deployment
- [ ] Graceful shutdown (30-60s timeout)
- [ ] Readiness probe
- [ ] Health check endpoint
- [ ] Deployment scripts or manifests
- [ ] Load balancer configuration

### Testing
- [ ] Unit tests
- [ ] Integration tests
- [ ] Load tests (before production)
- [ ] Chaos/failure tests (optional)

### Monitoring
- [ ] Alert rules for key metrics
- [ ] Log aggregation setup
- [ ] Incident response plan
- [ ] Runbook for common issues
```

---

## Quick start

1. **Use the template:** Copy `reference/standard-service` or `examples/standard-api`
2. **Add middleware:**
   ```go
   app.Use(requestid.Handler())
   app.Use(securityheaders.Handler())
   app.Use(recovery.Handler())
   app.Use(accesslog.Handler(logger))
   ```
3. **Add health checks:** Copy `/health` and `/readiness` from reference/standard-service
4. **Add metrics:** Use `metrics` module, wire to Prometheus/OTel
5. **Test:** `go test ./...` and run locally

That's it. You now have a production-ready foundation. Build on top of it.

---

For more details on any module, see `docs/modules/`.  
For deployment specifics, see your platform's documentation (Kubernetes, Docker, AWS, etc.).
