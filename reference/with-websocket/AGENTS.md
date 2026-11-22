# AGENTS.md - reference/with-websocket

Operational guide for agents working in `reference/with-websocket`.

This file is intentionally short. The repository root `AGENTS.md` remains
authoritative; when a task becomes architectural, security-sensitive, or
cross-module, fall back to the root workflow and the matching
`specs/task-routing.yaml` entry.

## 1. Minimal Context

For changes confined to this directory, load only:

- repository root `AGENTS.md`, if not already loaded
- this file
- the touched Go files and their tests

Read `reference/standard-service` first — this service has the same core shape
and extends it with WebSocket support.

## 2. Purpose And Boundaries

`reference/with-websocket` demonstrates a WebSocket server with broadcast support
using `x/websocket`. WebSocket endpoints are auto-registered by
`websocket.Server.RegisterRoutes()`. A JWT secret authenticates connection
requests; the demo uses `AllowUnauthenticated: true` for simplicity — production
must set `WS_SECRET` to a strong 32-byte minimum value.

Hard rules:

- `x/websocket` is the only allowed `x/*` import.
- No new third-party dependencies.
- No hidden globals, `init()` registration, or reflection routing.
- Keep `main.go` thin: load config, construct app, register routes, start.
- Keep middleware wiring explicit in `internal/app/app.go`.
- WebSocket routes are auto-registered via `websocket.Server.RegisterRoutes()` —
  this is the intended pattern, not a violation of explicit routing.
- `WS_SECRET` must be at least 32 bytes; the service must refuse to start with a
  shorter value.
- Graceful shutdown must call `a.WS.Shutdown()` with a timeout.

## 3. Package Ownership

- `main.go`: process entrypoint only.
- `internal/config`: config loading, defaults, and environment (APP_ADDR, WS_SECRET,
  APP_DEBUG, APP_ENV_FILE).
- `internal/app/app.go`: middleware chain (requestid → accesslog → recovery),
  graceful shutdown (includes WS.Shutdown()).
- `internal/app/routes.go`: GET /healthz (inline), WebSocket route registration.

Dependency direction:

```text
main.go → config → app → x/websocket
```

## 4. Change Patterns

Enable JWT authentication:

1. Set `WS_SECRET` to a 32-byte minimum value in config (or `.env`).
2. Set `AllowUnauthenticated: false` in the `websocket.Server` options in `routes.go`.
3. Clients must send a valid JWT in the WebSocket upgrade request.

Add a message handler:

1. Configure a `websocket.MessageHandler` in `routes.go` to process received messages.
2. The handler receives the raw message bytes and a reference to the connection.
3. Add a focused test using a WebSocket test client.

Add a health check for the WebSocket server:

1. Implement `health.ComponentChecker` and register it before `Prepare()`.
2. `/readyz` reflects registered checkers automatically.

## 5. Validation

For docs-only changes:

```bash
git diff --check
```

For Go changes inside this service:

```bash
cd reference/with-websocket && go test -race -timeout 30s ./...
go run ./internal/checks/dependency-rules
```

## 6. Review Focus

When reviewing or optimizing this service, check:

- `WS_SECRET` is validated as at least 32 bytes at startup; service must not start
  with a shorter value.
- `x/websocket` only — no other `x/*` packages.
- `AllowUnauthenticated: true` is acceptable for the demo; flag it clearly for
  production where JWT validation is required.
- Graceful shutdown drains active connections via `WS.Shutdown()` with a timeout.
- No message payload logs contain credentials or sensitive user data.
