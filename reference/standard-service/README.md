# Standard Service Reference

`reference/standard-service` is Plumego's canonical reference application and the third
bootstrap form described in `docs/start/getting-started.md` — **recommended for production services**.

Read this immediately after `docs/start/getting-started.md`. This directory is the
default answer to "what should a Plumego application look like?"

It is the single source of truth for:

- default application layout
- bootstrap flow in `main.go`
- route registration style
- handler and response conventions
- minimal stable-root-only wiring

Design constraints:

- depends only on `core`, `router`, `contract`, `middleware`, `health`, `metrics`, and `log` plus the standard library
- excludes `x/*` capabilities from the canonical learning path
- keeps all route registration explicit in `internal/app/routes.go`
- keeps app-local configuration in `internal/config`

## Not included

This reference intentionally excludes capabilities that belong in the matching
`reference/with-*` apps. Add them there rather than expanding this canonical baseline:

| Need                        | Starting point                                            |
|-----------------------------|-----------------------------------------------------------|
| Authentication / authz      | `reference/production-service`                            |
| Database connections        | swap `item.MemoryStore` for a DB-backed `item.Repository` |
| Multi-tenancy               | `reference/with-tenant`                                   |
| WebSocket                   | `reference/with-websocket`                                |
| gRPC / RPC                  | `reference/with-rpc`                                      |
| AI / LLM integration        | `reference/with-ai`                                       |
| Frontend asset serving      | `reference/with-frontend`                                 |
| Metrics export (Prometheus) | `reference/with-ops`                                      |
| Distributed tracing         | `reference/with-observability`                            |

No `x/*` extension packages are imported. Automatic route discovery, reflection routing,
and controller scanning are absent by design — every route is declared explicitly in
`internal/app/routes.go`.

## Directory layout

```
reference/standard-service/
├── main.go                       startup only (load config → build app → register routes → start)
├── env.example                   all supported environment variables with defaults
├── Dockerfile                    multi-stage build; context must be the repo root
├── internal/
│   ├── config/
│   │   └── config.go             Config struct, Defaults(), Load(), Validate()
│   ├── app/
│   │   ├── app.go                App struct, New() (middleware wiring), Start()
│   │   └── routes.go             RegisterRoutes() — every public path is declared here
│   ├── handler/
│   │   ├── api.go                APIHandler: Root, Hello, Info, Greet
│   │   ├── health.go             HealthHandler: Live (/healthz), Ready (/readyz)
│   │   ├── items.go              ItemHandler: Create, List, GetByID, Update, Patch, Delete
│   │   ├── guard.go              RequireWriteKey — per-route middleware for mutating ops
│   │   └── write.go              logWriteErr helper
│   └── domain/
│       └── item/
│           ├── item.go           Item domain model
│           ├── store.go          Repository interface + MemoryStore (thread-safe, in-memory)
│           └── service.go        Service interface + ItemService (normalises input)
└── ARCHITECTURE.md               layout rationale, dependency direction, pattern explanations
```

Dependency direction is one-way:

```
main.go → internal/config → internal/app → internal/handler → internal/domain/item
```

`internal/handler` never imports `internal/app` or `internal/config`; handlers are
independently testable by injecting stubs.

## Quick start

```bash
cd reference/standard-service

# Optional: copy env.example to .env and edit — the service works without it
cp env.example .env

go run .
```

With the default config the server logs two startup warnings reminding you to set
`APP_WRITE_KEY` and `APP_CORS_ALLOWED_ORIGINS` before deploying. Both are expected
on a clean checkout. See `env.example` for all configuration variables.

The server starts on `:8080`. The first request:

```bash
curl http://localhost:8080/healthz
```

```json
{"data":{"status":"ok","service":"plumego-reference","timestamp":"2024-01-01T00:00:00Z"},"request_id":"..."}
```

To change the listen address or service name without editing code:

```bash
APP_ADDR=:9090 APP_SERVICE_NAME=myservice go run .
```

All supported variables are documented in `env.example`.

## Configuration precedence

```
Defaults() < .env file < process environment < command-line flags
```

`config.Load()` applies these layers in order. Each layer overrides the previous.
The `--env-file` flag and `APP_ENV_FILE` variable control which `.env` file is loaded;
the default is `.env` in the working directory (silently absent is fine).

Add a config field by:
1. Adding the field to `AppConfig` in `internal/config/config.go`
2. Setting a safe default in `Defaults()`
3. Reading it in `applyEnv()` — one function handles `.env`, process env, and test stubs
4. Optionally adding a flag in `applyFlags()` — `filterFlagArgs` picks it up automatically
5. Using the value in `routes.go` or passing it to a handler constructor

## Middleware stack

```
requestid → security → cors → recovery → accesslog → bodylimit → httpmetrics → timeout
```

Order matters. The comment block in `internal/app/app.go` explains each position.
The full stack is wired once in `New()` and applies to every route.

- `requestid` stamps a correlation ID before any logging or error handling
- `security` sets `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy` on every response
- `cors` handles preflight; defaults to `*` — set `APP_CORS_ALLOWED_ORIGINS` in production
- `recovery` converts panics to 500 responses
- `accesslog` logs every request/response after recovery so panics appear as 500
- `bodylimit` rejects oversized bodies with 413 (default: 1 MiB)
- `httpmetrics` measures handler latency; swap `NewNoopCollector` for a real collector
- `timeout` imposes a per-request wall-clock limit (default: 30 s), innermost so it covers only handler time

Production services extend this stack with rate limiting, auth adapters, and tracing as
application-local decisions. Do not replace visible wiring with a hidden global bundle.

## curl examples

The server must be running (`go run .`) before running these commands.
All mutating endpoints (`POST`, `PUT`, `PATCH`, `DELETE`) are open by default.
Set `APP_WRITE_KEY=mysecret` to require `X-Write-Key: mysecret` on those requests.

### Service discovery

```bash
# Root: minimal service identity
curl http://localhost:8080/
# {"data":{"service":"plumego-reference","version":"dev","docs":"/api/hello"},"request_id":"..."}

# Full endpoint list (hand-authored; TestHelloEndpointListMatchesRegisteredRoutes detects drift)
curl http://localhost:8080/api/hello
# {"data":{"message":"hello from plumego standard-service","service":"...","version":"...","endpoints":[...]},...}

# Application wiring info
curl http://localhost:8080/api/info
# {"data":{"service":"plumego-reference","version":"dev","timestamp":"2024-01-01T00:00:00Z"},"request_id":"..."}

# Liveness probe (always 200 while the process is alive — no dependency checks)
curl http://localhost:8080/healthz
# {"data":{"status":"ok","service":"plumego-reference","timestamp":"2024-01-01T00:00:00Z"},"request_id":"..."}

# Readiness probe (probes all registered ComponentCheckers; 503 when any fail)
curl http://localhost:8080/readyz
# {"data":{"ready":true,"timestamp":"2024-01-01T00:00:00Z","components":{"item_store":true}},"request_id":"..."}
```

### Greeting (demonstrates TypeRequired error path)

```bash
# Success
curl "http://localhost:8080/api/v1/greet?name=Alice"
# {"data":{"message":"hello, Alice"},"request_id":"..."}

# Missing parameter — structured 400 error
curl "http://localhost:8080/api/v1/greet"
# {"error":{"code":"greet.name.required","message":"name is required",...},"request_id":"..."}
```

### Items CRUD

```bash
# Create
curl -X POST http://localhost:8080/api/v1/items \
     -H "Content-Type: application/json" \
     -d '{"name":"widget","description":"a small widget"}'
# 201 → {"data":{"id":"...","name":"widget","description":"a small widget","created_at":"..."},...}

# List (with pagination)
curl "http://localhost:8080/api/v1/items"
curl "http://localhost:8080/api/v1/items?limit=5&offset=10"

# Get by ID (replace <id> with the id from Create)
curl http://localhost:8080/api/v1/items/<id>
# 404 example:
curl http://localhost:8080/api/v1/items/no-such-id
# {"error":{"code":"item.not_found","message":"item not found",...},"request_id":"..."}

# Full update — replaces all mutable fields
curl -X PUT http://localhost:8080/api/v1/items/<id> \
     -H "Content-Type: application/json" \
     -d '{"name":"renamed widget","description":"updated description"}'

# Partial update — updates only the non-empty fields in the body
curl -X PATCH http://localhost:8080/api/v1/items/<id> \
     -H "Content-Type: application/json" \
     -d '{"name":"just rename"}'

# Delete — returns 204 No Content
curl -X DELETE http://localhost:8080/api/v1/items/<id>
```

### With write key enabled

```bash
# Start with a key:
APP_WRITE_KEY=mysecret go run .

# Guarded request — succeeds
curl -X POST http://localhost:8080/api/v1/items \
     -H "Content-Type: application/json" \
     -H "X-Write-Key: mysecret" \
     -d '{"name":"widget","description":"a widget"}'

# Missing header — 401
curl -X POST http://localhost:8080/api/v1/items \
     -H "Content-Type: application/json" \
     -d '{"name":"widget","description":"a widget"}'
# {"error":{"code":"auth.key.invalid","message":"valid X-Write-Key header required",...},...}
```

### Error shape examples

```bash
# Validation — multiple field errors in one response
curl -X POST http://localhost:8080/api/v1/items \
     -H "Content-Type: application/json" \
     -d '{}'
# {"error":{"code":"item.fields.required","message":"one or more required fields are missing",
#            "details":{"description":"field is required","name":"field is required"}},...}

# Unknown field — strict decoding rejects typos
curl -X POST http://localhost:8080/api/v1/items \
     -H "Content-Type: application/json" \
     -d '{"name":"widget","desc":"a widget"}'
# {"error":{"code":"item.create.unknown_field","details":{"field":"desc"},...},...}

# Oversized body (default 1 MiB limit)
curl -X POST http://localhost:8080/api/v1/items \
     -H "Content-Type: application/json" \
     -d "$(python3 -c "print('{\"name\":\"' + 'x'*2000000 + '\"}')")"
# 413 Request Entity Too Large
```

## Response shape

Every endpoint uses a single canonical envelope via `contract.WriteResponse` and
`contract.WriteError`. No ad hoc JSON helpers or per-handler response structs.

**Success** (`contract.WriteResponse` — only accepts 2xx status codes):
```json
{
  "data":       { "id": "...", "name": "widget", "description": "...", "created_at": "..." },
  "meta":       { "total": 42, "limit": 20, "offset": 0 },
  "request_id": "01HX..."
}
```
`data` and `meta` use `omitempty`: `meta` is absent when `nil` is passed;
`data` is absent when `nil`. For 204 No Content the body is suppressed entirely
by the contract layer — `writeJSON` writes only the status line.

**Error** (`contract.WriteError`):
```json
{
  "error": {
    "code":     "item.fields.required",
    "message":  "one or more required fields are missing",
    "category": "validation_error",
    "type":     "required_field_missing",
    "details":  { "name": "field is required", "description": "field is required" }
  },
  "request_id": "01HX..."
}
```
`type` is the `ErrorType` string value (e.g. `TypeRequired = "required_field_missing"`),
not the Go identifier. `category` is derived automatically from the type
(`TypeRequired → "validation_error"`, `TypeNotFound → "client_error"`,
`TypeUnauthorized → "auth_error"`, `TypeUnavailable → "server_error"`). `code`
is overridden per handler with a namespaced string (`<resource>.<operation>.<reason>`).
`details` carries per-field annotations; it is omitted when empty.

## Lifecycle

`main.go` wires `signal.NotifyContext(SIGINT, SIGTERM)` and passes the context to
`app.Start(ctx)`. When a signal fires:

1. The context is cancelled.
2. `app.Start` triggers `core.Shutdown` with a 15 s deadline.
3. In-flight requests are allowed to complete within that window.
4. `app.Start` returns `nil` on clean shutdown, or an error if the deadline expires.
5. `main.run` returns; the process exits.

The full bootstrap sequence in `main.go` is:

```
config.Load()           → validated Config
app.New(cfg)            → App (middleware wired)
a.RegisterRoutes()      → route table frozen
a.Start(ctx)            → server running; blocks until context cancels
```

Each step is a distinct function so failures are identified precisely. `main.go`
contains exactly these four calls and no logic.

## Where to add things

### A new endpoint

1. Add a handler method in `internal/handler/` (new file or an existing one).
2. Add domain logic in `internal/domain/<name>/` if needed.
3. Declare the handler's dependency as an interface field; wire concrete types from `routes.go`.
4. Register in `internal/app/routes.go` with one method + one path + one handler per line.
5. Add focused tests in `internal/handler/handler_test.go`.

See `AGENT_TASKS.md` for copy-paste templates.

### A new config field

Add to `AppConfig` in `internal/config/config.go`, set a default in `Defaults()`,
read it in `applyEnv()`, and use it in `routes.go` or pass it to a handler constructor.

### Global middleware

Add to `app.Use(...)` in `internal/app/app.go` at the correct position. Annotate the
order comment if the position matters. All middleware here runs on every route.

### Per-route middleware

Wrap the handler at the call site in `routes.go`:

```go
v1.post("/items", writeGuard(http.HandlerFunc(items.Create)))
```

`writeGuard` is the existing example. For route-group protection, wrap every handler in
the group rather than modifying `app.New`.

### A new readiness probe

Implement `health.ComponentChecker` and add it to `HealthHandler.Checkers` in
`routes.go`. See `ARCHITECTURE.md §Readiness checking` for the full pattern.

## Running the tests

```bash
cd reference/standard-service && go test -race -timeout 30s ./...
```

The test suite covers all handler paths, config precedence, domain behaviour, context
cancellation, middleware wiring, CORS, security headers, and graceful shutdown.

## Running as a container

Build context must be the repository root (the `replace` directive in `go.mod` requires it):

```bash
docker build \
  -f reference/standard-service/Dockerfile \
  --build-arg VERSION=$(git describe --tags --always) \
  -t standard-service .

docker run --rm -p 8080:8080 \
  -e APP_WRITE_KEY=mysecret \
  -e APP_CORS_ALLOWED_ORIGINS=https://app.example.com \
  standard-service
```

## Canonical scaffold template

```bash
plumego new myapp --template canonical
```

The scaffold preserves the same bootstrap, `internal/app`, `internal/handler`, and
`internal/config` runtime shape, while adapting the entrypoint to a generated project
layout (`cmd/app/main.go`) and adding project-local files such as `go.mod`,
`env.example`, `.gitignore`, and `README.md`. Reference-only tests stay in this
directory; generated projects should add their own tests next to changed behaviour.

To copy manually without the scaffold, change three things:

1. Rename the module in `go.mod` from `standard-service` to your module path
   (e.g. `module github.com/yourorg/myapp`).
2. Remove the `replace` directive and pin the real published version of
   `github.com/birdordev/plumego`.
3. Replace the `standard-service/` import prefix in all `.go` files with your
   new module path.

## Production readiness

See `PRODUCTION_CHECKLIST.md` before promoting to production. Key items:
- Set `APP_WRITE_KEY` — empty means publicly writable mutations (a startup WARN is emitted)
- Set `APP_CORS_ALLOWED_ORIGINS` — empty means wildcard `*` (a startup WARN is emitted)
- Tighten `core.ReadTimeout`, `core.WriteTimeout`, `core.IdleTimeout` from their permissive defaults
- Add rate limiting (`middleware/abuseguard`) on public endpoints
- Swap `metrics.NewNoopCollector()` for a real collector if you need HTTP metrics

## Canonical next reads

1. `docs/README.md`
2. `docs/reference/canonical-style-guide.md`
3. `docs/concepts/agent-first-repo-blueprint.md`
4. `specs/task-routing.yaml`
