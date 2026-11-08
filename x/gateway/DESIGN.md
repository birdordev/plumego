# Reverse Proxy Design

## Overview

This document describes the design of the reverse proxy middleware for plumego, enabling API gateway functionality.

## Architecture

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │ Request
       ▼
┌─────────────────────────────────────┐
│  Proxy Middleware                   │
│  ┌───────────────────────────────┐  │
│  │ 1. Request Transformation     │  │
│  │    - Path rewrite             │  │
│  │    - Header modification      │  │
│  │    - Body inspection          │  │
│  └───────────────────────────────┘  │
│  ┌───────────────────────────────┐  │
│  │ 2. Service Discovery          │  │
│  │    - Static targets           │  │
│  │    - Dynamic discovery        │  │
│  └───────────────────────────────┘  │
│  ┌───────────────────────────────┐  │
│  │ 3. Load Balancing             │  │
│  │    - Round robin              │  │
│  │    - Weighted                 │  │
│  │    - Least connections        │  │
│  │    - IP hash                  │  │
│  └───────────────────────────────┘  │
│  ┌───────────────────────────────┐  │
│  │ 4. Health Check               │  │
│  │    - Active probing           │  │
│  │    - Passive detection        │  │
│  └───────────────────────────────┘  │
│  ┌───────────────────────────────┐  │
│  │ 5. HTTP Transport             │  │
│  │    - Connection pooling       │  │
│  │    - Keep-alive               │  │
│  │    - Retry logic              │  │
│  └───────────────────────────────┘  │
│  ┌───────────────────────────────┐  │
│  │ 6. Response Transformation    │  │
│  │    - Header modification      │  │
│  │    - Body transformation      │  │
│  └───────────────────────────────┘  │
└─────────────────────────────────────┘
       │ Proxied Request
       ▼
┌─────────────────┐
│  Backend Service│
└─────────────────┘
```

## Core Components

### 1. Proxy Config

```go
type Config struct {
    // Targets: Static list of backend URLs
    Targets []string

    // ServiceName: For dynamic service discovery
    ServiceName string

    // Discovery: Service discovery interface
    Discovery ServiceDiscovery

    // LoadBalancer: Load balancing strategy
    LoadBalancer LoadBalancer

    // Transport: HTTP transport configuration
    Transport *TransportConfig

    // PathRewrite: Function to rewrite request path
    PathRewrite PathRewriteFunc

    // ModifyRequest: Hook to modify outgoing request
    ModifyRequest RequestModifier

    // ModifyResponse: Hook to modify incoming response
    ModifyResponse ResponseModifier

    // ErrorHandler: Custom error handling
    ErrorHandler ErrorHandlerFunc

    // WebSocketEnabled: Enable WebSocket proxying
    WebSocketEnabled bool
}
```

### 2. Load Balancer Interface

We'll reuse and adapt the existing `x/data/rw.LoadBalancer` interface:

```go
type LoadBalancer interface {
    // Next selects the next backend
    Next(backends []Backend) (int, error)

    // Reset resets the load balancer state
    Reset()

    // Name returns the strategy name
    Name() string
}

type Backend struct {
    URL       string
    Weight    int
    IsHealthy bool
    Metadata  map[string]string
}
```

### 3. Service Discovery Interface

```go
type ServiceDiscovery interface {
    // Resolve returns the list of backend URLs for a service
    Resolve(ctx context.Context, serviceName string) ([]string, error)

    // Watch returns a channel that receives backend updates
    Watch(ctx context.Context, serviceName string) (<-chan []string, error)

    // Register registers a service instance
    Register(ctx context.Context, instance ServiceInstance) error

    // Deregister removes a service instance
    Deregister(ctx context.Context, serviceID string) error

    // Health updates the health status of an instance
    Health(ctx context.Context, serviceID string, healthy bool) error
}

type ServiceInstance struct {
    ID       string
    Name     string
    Address  string
    Port     int
    Metadata map[string]string
    Tags     []string
}
```

### 4. Health Checker

```go
type HealthChecker struct {
    // Backends: List of backends to check
    Backends []*Backend

    // Interval: Check interval
    Interval time.Duration

    // Timeout: Health check timeout
    Timeout time.Duration

    // Path: Health check endpoint path
    Path string

    // Method: HTTP method (default: GET)
    Method string

    // ExpectedStatus: Expected status code (default: 200)
    ExpectedStatus int

    // OnHealthChange: Callback when health status changes
    OnHealthChange func(backend *Backend, healthy bool)
}
```

### 5. Transport Pool

```go
type TransportPool struct {
    // Transports: Map of target URL to transport
    transports map[string]*http.Transport

    // Config: Transport configuration
    config *TransportConfig

    mu sync.RWMutex
}

type TransportConfig struct {
    // MaxIdleConns: Maximum idle connections
    MaxIdleConns int

    // MaxIdleConnsPerHost: Maximum idle connections per host
    MaxIdleConnsPerHost int

    // IdleConnTimeout: Idle connection timeout
    IdleConnTimeout time.Duration

    // ResponseHeaderTimeout: Response header timeout
    ResponseHeaderTimeout time.Duration

    // TLSHandshakeTimeout: TLS handshake timeout
    TLSHandshakeTimeout time.Duration

    // DisableKeepAlives: Disable HTTP keep-alives
    DisableKeepAlives bool

    // DisableCompression: Disable compression
    DisableCompression bool
}
```

## Request Flow

### HTTP Request Flow

1. **Receive Request**: Client sends HTTP request
2. **Request Transformation**: Apply `PathRewrite` and `ModifyRequest`
3. **Service Resolution**: Get backends from static list or service discovery
4. **Load Balancing**: Select backend using configured strategy
5. **Health Check**: Verify backend is healthy (passive)
6. **Proxy Request**: Forward request using HTTP transport
7. **Retry Logic**: Retry on failure (using existing `net/http.Client`)
8. **Response Transformation**: Apply `ModifyResponse`
9. **Return Response**: Send response back to client

### WebSocket Flow

1. **Upgrade Request**: Detect WebSocket upgrade request
2. **Backend Selection**: Select backend (same as HTTP)
3. **Dial Backend**: Establish WebSocket connection to backend
4. **Bidirectional Proxy**: Copy data between client and backend
5. **Connection Management**: Handle ping/pong, close frames

## Usage Examples

### Simple Static Proxy

```go
import (
    "context"
    "github.com/birdordev/plumego/core"
    "github.com/birdordev/plumego/x/gateway"
)

cfg := core.DefaultConfig()
app := core.New(cfg, core.AppDependencies{})

// Proxy to static backends
if err := app.Any("/api/users/*path", gateway.New(gateway.Config{
    Targets: []string{
        "http://user-service-1:8080",
        "http://user-service-2:8080",
    },
    LoadBalancer: gateway.NewRoundRobinBalancer(),
})); err != nil {
    panic(err)
}

if err := app.Prepare(); err != nil {
    panic(err)
}
srv, err := app.Server()
if err != nil {
    panic(err)
}
defer app.Shutdown(context.Background())
if err := srv.ListenAndServe(); err != nil {
    panic(err)
}
```

### With Service Discovery

```go
import (
    "context"
    "github.com/birdordev/plumego/core"
    "github.com/birdordev/plumego/x/gateway"
    "github.com/birdordev/plumego/x/gateway/discovery"
)

// Create Consul discovery client
consulSD := discovery.NewConsul("localhost:8500")

cfg := core.DefaultConfig()
app := core.New(cfg, core.AppDependencies{})

// Proxy using service discovery
if err := app.Any("/api/orders/*path", gateway.New(gateway.Config{
    ServiceName: "order-service",
    Discovery: consulSD,
    LoadBalancer: gateway.NewWeightedRoundRobinBalancer(),
    PathRewrite: gateway.StripPrefix("/api/orders"),
})); err != nil {
    panic(err)
}

if err := app.Prepare(); err != nil {
    panic(err)
}
srv, err := app.Server()
if err != nil {
    panic(err)
}
defer app.Shutdown(context.Background())
if err := srv.ListenAndServe(); err != nil {
    panic(err)
}
```

### With Request/Response Modification

```go
app.Use("/api/v1/*", proxy.New(proxy.Config{
    Targets: []string{"http://backend:8080"},

    ModifyRequest: func(req *http.Request) error {
        // Add custom headers
        req.Header.Set("X-Gateway", "plumego")
        req.Header.Set("X-Forwarded-Host", req.Host)
        return nil
    },

    ModifyResponse: func(resp *http.Response) error {
        // Add security headers
        resp.Header.Set("X-Content-Type-Options", "nosniff")
        return nil
    },
}))
```

### With Custom Error Handling

```go
app.Use("/api/*", proxy.New(proxy.Config{
    Targets: []string{"http://backend:8080"},

    ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
        log.Printf("Proxy error: %v", err)

        if errors.Is(err, proxy.ErrNoHealthyBackends) {
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]string{
                "error": "Service temporarily unavailable",
            })
            return
        }

        w.WriteHeader(http.StatusBadGateway)
        json.NewEncoder(w).Encode(map[string]string{
            "error": "Gateway error",
        })
    },
}))
```

### With WebSocket Support

```go
app.Use("/ws/*", proxy.New(proxy.Config{
    Targets: []string{"ws://backend:8080"},
    WebSocketEnabled: true,
    PathRewrite: proxy.StripPrefix("/ws"),
}))
```

## Integration Points

### 1. Existing HTTP Client
- Reuse `net/http.Client` with retry logic
- Leverage existing retry policies
- Use existing middleware support

### 2. Existing Load Balancers
- Adapt `x/data/rw.LoadBalancer` interface
- Reuse round-robin, weighted, least-connections strategies
- Add IP hash strategy

### 3. Health Check System
- Integrate with `health` package
- Report backend health status
- Use existing health check infrastructure

### 4. Metrics Collection
- Integrate with `metrics` package
- Track proxy request latency
- Monitor backend health
- Count retry attempts

### 5. Middleware Chain
- Proxy is a standard middleware
- Can be combined with auth, rate limiting, logging
- Respects middleware order

## File Structure

```
middleware/proxy/
├── DESIGN.md              # This file
├── proxy.go               # Core proxy middleware
├── config.go              # Configuration types
├── backend.go             # Backend management
├── balancer.go            # Load balancer adapters
├── health.go              # Health checking
├── transport.go           # Transport pool management
├── rewrite.go             # Path rewriting utilities
├── modifier.go            # Request/response modifiers
├── websocket.go           # WebSocket proxying
├── error.go               # Error types and handlers
├── proxy_test.go          # Unit tests
└── example_test.go        # Example usage

net/discovery/
├── discovery.go           # Service discovery interface
├── static.go              # Static configuration
├── consul.go              # Consul adapter
├── kubernetes.go          # Kubernetes adapter (future)
├── etcd.go                # etcd adapter (future)
└── discovery_test.go      # Tests
```

## Performance Considerations

1. **Connection Pooling**: Reuse HTTP connections via `http.Transport`
2. **Keep-Alive**: Enable HTTP keep-alive by default
3. **Buffer Pooling**: Use sync.Pool for request/response buffers
4. **Zero-Copy**: Use `io.Copy` for body streaming
5. **Minimal Allocation**: Avoid unnecessary object creation
6. **Backend Caching**: Cache service discovery results with TTL

## Error Handling

### Error Types
- `ErrNoBackends`: No backends configured
- `ErrNoHealthyBackends`: All backends are unhealthy
- `ErrBackendTimeout`: Backend did not respond in time
- `ErrBackendRefused`: Backend refused connection
- `ErrTooManyRetries`: Exhausted all retry attempts

### Error Strategies
1. **Fail Fast**: Return error immediately (no retries)
2. **Retry**: Attempt request on different backends
3. **Circuit Breaker**: Open circuit after consecutive failures
4. **Fallback**: Return cached response or default value

## Security Considerations

1. **Header Sanitization**: Remove sensitive headers (Authorization, Cookie)
2. **Path Traversal**: Validate rewritten paths
3. **Request Size Limits**: Enforce body size limits
4. **Timeout Enforcement**: Prevent slow loris attacks
5. **TLS Verification**: Verify backend certificates
6. **Rate Limiting**: Apply per-backend rate limits

## Future Enhancements

1. **Circuit Breaker**: Integrate with P1 circuit breaker
2. **Response Caching**: Cache backend responses
3. **Request Coalescing**: Merge duplicate in-flight requests
4. **Streaming**: Support HTTP/2 server push
5. **gRPC Proxy**: Support gRPC protocol
6. **Metrics Dashboard**: Real-time proxy statistics
7. **Dynamic Config**: Hot-reload proxy configuration

## Testing Strategy

1. **Unit Tests**: Test individual components in isolation
2. **Integration Tests**: Test with real HTTP servers
3. **Load Tests**: Benchmark throughput and latency
4. **Chaos Tests**: Test failure scenarios (backend down, timeout, etc.)
5. **WebSocket Tests**: Test bidirectional streaming

## Migration Guide

For users migrating from other API gateways:

### From Kong
```go
// Kong: kong.yml
routes:
  - name: user-service
    paths: [/api/users]
    service: user-service

// Plumego equivalent
app.Use("/api/users/*", proxy.New(proxy.Config{
    ServiceName: "user-service",
    Discovery: consulSD,
}))
```

### From Traefik
```yaml
# Traefik: traefik.yml
http:
  routers:
    user-router:
      rule: "PathPrefix(`/api/users`)"
      service: user-service
```

```go
// Plumego equivalent
app.Use("/api/users/*", proxy.New(proxy.Config{
    ServiceName: "user-service",
    Discovery: consulSD,
}))
```

## Implementation Phases

### Phase 1: Core Proxy (Day 1-2)
- [ ] Basic HTTP proxying
- [ ] Static backend list
- [ ] Path rewriting
- [ ] Request/response modification

### Phase 2: Load Balancing (Day 2-3)
- [ ] Load balancer interface
- [ ] Round-robin strategy
- [ ] Weighted strategy
- [ ] IP hash strategy

### Phase 3: Health & Transport (Day 3)
- [ ] Health checking
- [ ] Transport pooling
- [ ] Connection reuse
- [ ] Retry integration

### Phase 4: WebSocket (Day 4)
- [ ] WebSocket detection
- [ ] Connection upgrade
- [ ] Bidirectional proxy
- [ ] Close handling

### Phase 5: Service Discovery (Day 5-7)
- [ ] Discovery interface
- [ ] Static provider
- [ ] Consul adapter
- [ ] Watch support

### Phase 6: Polish (Day 7)
- [ ] Error handling
- [ ] Metrics integration
- [ ] Documentation
- [ ] Examples
