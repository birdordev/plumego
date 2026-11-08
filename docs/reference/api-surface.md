# Plumego Public API Surface

This document inventories exported symbols across Plumego's stable roots and major extensions. Use this as a quick reference or for version tracking.

## Stable Roots v1 API Inventory

Generated for v1.1.0. These symbols are frozen for `v1.x`; breaking changes require `v2.0`.

### `core` (118 exported symbols)

**Types:** `App`, `AppConfig`, `AppDependencies`, `Config`, `DefaultConfig()`, `Lifecycle`, `LifecycleEvent`, `LifecycleEventType`, `LifecycleGroup`, `RouteGroup`

**Functions (primary):**
- `New(config Config, deps AppDependencies) *App`
- `NewWithConfig(cfg Config) *App`
- `(app *App) Get(path string, handler http.Handler)`
- `(app *App) Post(path string, handler http.Handler)`
- `(app *App) Put(path string, handler http.Handler)`
- `(app *App) Delete(path string, handler http.Handler)`
- `(app *App) Patch(path string, handler http.Handler)`
- `(app *App) Head(path string, handler http.Handler)`
- `(app *App) Options(path string, handler http.Handler)`
- `(app *App) AddRoute(method, path string, handler http.Handler)`
- `(app *App) Group(prefix string) *RouteGroup`
- `(app *App) Use(middleware func(http.Handler) http.Handler)`
- `(app *App) Prepare() error`
- `(app *App) Server() *http.Server`
- `(app *App) Shutdown(ctx context.Context) error`
- `(app *App) ServeHTTP(w http.ResponseWriter, r *http.Request)`

**Functions (secondary/introspection):** 40+ additional methods (see godoc)

**Key constants:** Server default address `:8080`, timeouts 30s, HTTP/2 enabled by default

[See core/doc.go for full details]

### `router` (223 exported symbols)

**Primary Types:** `Router`, `RouteInfo`, `Group`, `Param`, `ParamValues`

**Key Functions:**
- `(router *Router) Get(path string, handler http.Handler)`
- `(router *Router) Post(path string, handler http.Handler)`
- `(router *Router) Group(prefix string) *Group`
- `(router *Router) Freeze()` — locks route registration
- `(router *Router) Match(method, path string) (*RouteInfo, []string)`

**Request Context Accessors:**
- `RouteInfo(r *http.Request) *RouteInfo` — get matched route metadata
- `Params(r *http.Request) ParamValues` — all path params

**Route Metadata:**
- `(info *RouteInfo) Name` — route name (optional)
- `(info *RouteInfo) Pattern` — registered path pattern
- `(info *RouteInfo) Handler` — registered handler

[See router/doc.go for full details]

### `contract` (183 exported symbols)

**Response Helpers (primary):**
- `WriteResponse(w http.ResponseWriter, r *http.Request, status int, data interface{}, err error) error`
- `WriteError(w http.ResponseWriter, r *http.Request, err error) error`
- `WriteJSON(w http.ResponseWriter, status int, data interface{}) error`
- `WriteText(w http.ResponseWriter, status int, text string) error`

**Request Binding:**
- `BindJSON(r *http.Request, v interface{}) error` — parse + validate JSON
- `Param(r *http.Request, key string) string` — extract path param
- `Params(r *http.Request) map[string]string` — all path params
- `Query(r *http.Request, key string) string` — query parameter
- `Header(r *http.Request, key string) string` — HTTP header

**Request Metadata:**
- `RequestID(r *http.Request) string` — request tracking ID (set by middleware/requestid)
- `User(r *http.Request) interface{}` — user/claims (set by auth middleware)

**HTTP Status Codes & Error Types:** 100+ constants and error builders

[See contract/doc.go for full details]

### `middleware` (Composition + Packages)

**Composition:**
- `Handler(name string, func(http.Handler) http.Handler) func(http.Handler) http.Handler`
- Wrapper for named middleware

**Standard Packages** (first-party middlewares):
- `recovery` — Catch panics, return 500 with error
- `timeout` — Request timeout enforcement
- `cors` — CORS header handling
- `accesslog` — HTTP request logging
- `requestid` — Add/extract request IDs
- `auth` — Auth adapter (JWT, API key, etc.)
- `tracing` — Request tracing integration
- `compression` — Gzip/deflate compression
- `securityheaders` — Security header defaults
- `bodylimit` — Request body size limiting
- `concurrencylimit` — Concurrent request limiting
- `conformance` — Protocol conformance checks

Each middleware package exports `Handler()` function and optional configuration types.

[See middleware/README.md for each package details]

### `security` (94 exported symbols)

**Auth:**
- `JWTHandler(publicKey interface{}, opts ...Option) func(http.Handler) http.Handler`
- `APIKeyHandler(keyFunc KeyFunc) func(http.Handler) http.Handler`

**Password:**
- `HashPassword(password string) (string, error)`
- `VerifyPassword(hash, password string) bool`

**Headers:**
- `SecurityHeadersHandler(opts ...Option) func(http.Handler) http.Handler`

**Abuse Guards:**
- `RateLimitHandler(limiter RateLimiter) func(http.Handler) http.Handler`
- `AbuseDetector` interface for custom abuse logic

[See security/doc.go for full details]

### `store` (Contract interfaces + In-memory implementations)

**Interfaces:**
- `Cache` — Get, Set, Delete, Exists, TTL
- `KVStore` — Get, Set, Delete, List
- `FileStore` — Open, Save, Delete, Exists
- `Database` — Query, Exec, Close
- `Idempotency` — GetResult, SaveResult, Exists

**In-Memory Implementations:**
- `NewMemoryCache()` — Ephemeral cache
- `NewMemoryKV()` — Ephemeral key-value store
- `NewMemoryFile()` — Ephemeral file storage

All in-memory implementations are suitable for testing or small services; production use external databases.

[See store/doc.go for full details]

### `health` (22 exported symbols)

**Status:**
- `Status` struct with `Status` enum (Healthy, Degraded, Unhealthy)
- `Components []Component` — dependency status list

**Types:**
- `ComponentChecker` interface for checking dependency health
- `Component` — name, status, message

**Constants:**
- `StatusHealthy`, `StatusDegraded`, `StatusUnhealthy`

[See health/doc.go for full details]

### `log` (117 exported symbols)

**Interface:**
- `StructuredLogger` interface with methods:
  - `Info(msg string, fields ...Field)`
  - `Error(msg string, fields ...Field)`
  - `Warn(msg string, fields ...Field)`
  - `Debug(msg string, fields ...Field)`

**Default Implementation:**
- `NewDefault()` — stdlib-backed structured logger
- `NewNoop()` — discards all logs (for testing)

**Field Types:**
- `String(key string, val string) Field`
- `Int(key string, val int64) Field`
- `Bool(key string, val bool) Field`
- `Error(key string, val error) Field`
- And 10+ other field types

[See log/doc.go for full details]

### `metrics` (55 exported symbols)

**Interfaces:**
- `Counter` — Increment, Add
- `Gauge` — Set, Inc, Dec
- `Histogram` — Observe
- `Collector` — Collect() for aggregation

**Factories:**
- `NewCounter(name string, opts ...Option) Counter`
- `NewGauge(name string, opts ...Option) Gauge`
- `NewHistogram(name string, opts ...Option) Histogram`

**Labels:**
- `Label` type for metric dimensions
- `WithLabels(labels ...Label) Option`

[See metrics/doc.go for full details]

## Major Beta Extensions API Surface

### `x/rest` — REST CRUD Controllers

**Controller Pattern:**
- `ResourceHandler` interface
- `Register(app *core.App, resource ResourceHandler, basePath string)`

**Standard endpoints:** GET (list, item), POST (create), PUT (update), DELETE (remove)

### `x/websocket` — WebSocket Hub

**Hub:**
- `NewHub()` — Create WebSocket hub
- `(hub *Hub) Upgrade(handler HandlerFunc) http.Handler`
- `(hub *Hub) Broadcast(msg []byte)`
- `(hub *Hub) Close()`

### `x/gateway` — Proxy & Gateway

**Gateway:**
- `New(routes ...Route) *Gateway`
- `(gw *Gateway) Handler() http.Handler`
- `Route(pattern, target string) Route`

### `x/tenant` — Multi-Tenancy

**Resolver:**
- `Resolver(fn ResolveFn) func(http.Handler) http.Handler`
- `IDFromContext(r *http.Request) string`
- `IsolationMiddleware() func(http.Handler) http.Handler`

### `x/observability` — Metrics & Tracing

**Exporters:**
- `NewPrometheusExporter()` — Prometheus metrics
- `NewOpenTelemetryExporter()` — OTel tracing

**Middleware:**
- `MetricsMiddleware(exporter Exporter) func(http.Handler) http.Handler`
- `TracingMiddleware(exporter Exporter) func(http.Handler) http.Handler`

### `x/frontend` — Asset Serving

**Handler:**
- `Handler(fs http.FileSystem, opts ...Option) http.Handler`
- Option: `WithEmbedded(fsys embed.FS)`
- Option: `WithCompression()`

### `x/messaging` — Queues & Pub/Sub

**Messaging:**
- `Publish(topic string, data interface{}) error`
- `Subscribe(topic string, handler Handler) Subscription`
- `Queue(name string) Queue` — job queue interface

---

## API Stability Notes

### v1 Guarantee

All symbols listed under "Stable Roots" carry a v1 guarantee: they will not change (new functions may be added, existing functions will not change signature or behavior).

Exceptions:
- Internal types (tagged with `// internal`) may change
- Unexported fields of exported types may change (Go's backward compatibility rule)
- Method receivers may gain new methods

### Beta Extension Guarantee

Symbols in beta extensions (x/rest, x/websocket, etc.) are stable across cited release references but may change in future minors with deprecation notice.

### Experimental Extension Guarantee

Symbols in experimental extensions (x/ai, x/data, etc.) may change in any minor version without notice.

---

## Finding What You Need

**I want to...**

- **Register routes** → core.App.Get/Post/Put/Delete, core.RouteGroup
- **Extract path params** → contract.Param
- **Bind JSON** → contract.BindJSON
- **Send responses** → contract.WriteResponse
- **Handle errors** → contract.WriteError
- **Add middleware** → app.Use + middleware.* packages
- **Log** → log.StructuredLogger interface
- **Metrics** → metrics.Counter/Gauge/Histogram interfaces
- **Health checks** → health.Status
- **Auth** → security.JWTHandler, security.APIKeyHandler
- **Storage** → store.Cache/KVStore interfaces
- **REST CRUD** → x/rest extension
- **WebSocket** → x/websocket extension
- **Multi-tenant** → x/tenant extension
- **Observability** → x/observability extension
- **Proxy/gateway** → x/gateway extension
- **Static files** → x/frontend extension
- **Messaging** → x/messaging extension

---

For full API documentation, use:
```bash
go doc github.com/birdordev/plumego/core
go doc github.com/birdordev/plumego/contract
go doc github.com/birdordev/plumego/x/rest
# etc.
```

Or visit [pkg.go.dev](https://pkg.go.dev/github.com/birdordev/plumego).

**Last updated:** v1.1.0  
**For API changes in newer versions, see COMPATIBILITY.md.**
