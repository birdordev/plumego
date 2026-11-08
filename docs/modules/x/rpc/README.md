# x/rpc

`x/rpc` is the optional RPC transport family for Plumego applications. It keeps
the Plumego-facing surface dependency-free: concrete RPC runtimes (gRPC,
Connect-Go, Twirp) and generated stubs live in application code or out-of-tree
adapters; `x/rpc` only provides lifecycle helpers, connection pooling,
interceptors, and an HTTP transcoder that bridges RPC handlers to standard
`net/http`.

Status: **experimental**. The API may change between minor releases.

---

## Package Map

| Package | Purpose |
|---|---|
| `x/rpc/server` | Wraps a caller-owned RPC runtime with explicit lifecycle helpers |
| `x/rpc/client` | Target-keyed connection pool and transport-neutral unary interceptors |
| `x/rpc/gateway` | Mounts caller-owned RPC handlers as ordinary `http.Handler` values |

Start with `x/rpc/server` for service hosting. Use `x/rpc/client` for
structured connection reuse. Use `x/rpc/gateway` when a service exposes both
HTTP and RPC on the same `core.App`.

---

## Server Lifecycle (`x/rpc/server`)

`server.Runtime` is the minimal interface a caller-owned RPC runtime must
satisfy. Any concrete runtime (e.g. `*grpc.Server`) that has `RegisterService`,
`Serve`, `GracefulStop`, and `Stop` methods fulfils it.

```go
import (
    "context"
    "net"

    rpcsrv "github.com/birdordev/plumego/x/rpc/server"
)

// Wrap any runtime that implements server.Runtime.
srv := rpcsrv.New(grpcServer)

// Register a service implementation — delegates to the runtime.
if err := srv.RegisterService(&MyService_ServiceDesc, &myServiceImpl{}); err != nil {
    log.Fatalf("register service: %v", err)
}

// Serve blocks until the runtime stops.
lis, _ := net.Listen("tcp", ":9090")
go func() {
    if err := srv.Serve(lis); err != nil {
        log.Printf("rpc serve: %v", err)
    }
}()

// Graceful shutdown with deadline.
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
if err := srv.GracefulStop(shutdownCtx); err != nil {
    log.Printf("rpc shutdown: %v", err)
}
```

`GracefulStop` waits for in-flight RPCs to finish. If the context deadline
passes first, it calls `Stop()` to force-close and returns the context error.

---

## Client Pool (`x/rpc/client`)

`client.Pool` reuses connections by target address. Call `Dial` to get an
existing connection or create a new one. `Close` closes all pooled connections
and marks the pool as closed for new requests.

```go
import (
    "context"

    rpcclient "github.com/birdordev/plumego/x/rpc/client"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
)

// Dialer is caller-owned; the pool is transport-neutral.
dialer := func(ctx context.Context, target string, opts ...rpcclient.DialOption) (rpcclient.Conn, error) {
    return grpc.DialContext(ctx, target, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

pool := rpcclient.New(dialer)

conn, err := pool.Dial(ctx, "payments-service:9090")
if err != nil {
    return fmt.Errorf("dial payments: %w", err)
}

// Use conn to construct a gRPC client stub.
paymentsClient := paymentspb.NewPaymentsClient(conn.(*grpc.ClientConn))

// Close all connections at shutdown.
defer pool.Close()
```

### Interceptors

Three ready-made unary interceptors are provided. Compose them by chaining
invokers:

```go
import rpcclient "github.com/birdordev/plumego/x/rpc/client"

logging := rpcclient.LoggingInterceptor(logger)
retry   := rpcclient.RetryInterceptor(3, "Unavailable", "ResourceExhausted")
tracing := rpcclient.TracingInterceptor(tracer)

// Compose: tracing wraps retry wraps logging wraps the actual invoker.
var invoker rpcclient.UnaryInvoker = actualGRPCInvoker
invoker = wrap(logging, wrap(retry, wrap(tracing, invoker)))
```

| Interceptor | What it does |
|---|---|
| `LoggingInterceptor(logger)` | Logs method, duration, and status code after each call |
| `RetryInterceptor(maxAttempts, codes...)` | Retries on the listed transport status codes up to `maxAttempts` times |
| `TracingInterceptor(tracer)` | Starts a client span, records the result, and ends the span |

`TraceMetadata(ctx)` extracts trace IDs from context into a transport-neutral
`map[string]string` suitable for forwarding as RPC metadata headers.

---

## HTTP Gateway (`x/rpc/gateway`)

`gateway.HTTPTranscoder` adapts caller-owned RPC HTTP handlers to standard
`net/http`. Use it when a Plumego app needs to mount gRPC-Web, Connect-Go, or
another HTTP-over-RPC handler alongside ordinary REST endpoints.

```go
import (
    rpcgw "github.com/birdordev/plumego/x/rpc/gateway"
)

transcoder := rpcgw.New("greeter-service")

// Register an RPC handler at an HTTP path.
// handler receives (w, r, params) — params is reserved for future path-param use.
if err := transcoder.Register(ctx, func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
    grpcWebHandler.ServeHTTP(w, r)
}, "POST /grpc.Greeter/SayHello"); err != nil {
    log.Fatalf("register transcoder: %v", err)
}

// Mount the transcoder's handler on a Plumego route group.
rpcGroup := app.Group("/rpc")
rpcGroup.Any("/*path", transcoder.Handler())
```

The gateway does not perform protocol translation. It adapts the handler
registration shape and enforces the method. Protocol framing (gRPC binary,
Connect JSON, etc.) is the responsibility of the caller-owned handler.

---

## Wiring with core.App

See `reference/with-rpc` for the canonical Plumego + gRPC wiring shape. The
key constraint is that gRPC and HTTP share the same `core.App` lifecycle:

```
core.New → app.Use(middleware) → app.Get/Any(rpc paths) → app.Prepare → app.Server
                                                                         ↑
                                 rpc.server.New(grpcServer).Serve(lis) ──┘ (parallel goroutine)
```

Run `cd reference/with-rpc && go run .` to see a working in-process example
using `bufconn` (no real network port).

---

## Boundary Rules

- `x/rpc` must not be imported by stable roots (`core`, `router`, `contract`,
  `middleware`, `security`, `store`, `health`, `log`, `metrics`).
- Concrete RPC runtimes, generated stubs, and service definitions belong in
  application code, not in `x/rpc` itself.
- `x/rpc/client` may import `x/observability` for `TracingInterceptor`; no
  other cross-extension imports are allowed.
- Service discovery integration belongs in `x/gateway/discovery`, not here.

---

## Validation

```bash
go test -race -timeout 60s ./x/rpc/...
go vet ./x/rpc/...
```

---

## Related

- `reference/with-rpc/README.md` — canonical in-process gRPC + HTTP wiring example
- `docs/modules/x/observability/README.md` — tracing primitives used by `TracingInterceptor`
- `x/gateway/discovery` — dynamic backend lookup for client-side load balancing
