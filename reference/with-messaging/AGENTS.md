# AGENTS.md - reference/with-messaging

Operational guide for agents working in `reference/with-messaging`.

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
and extends it with in-process messaging.

## 2. Purpose And Boundaries

`reference/with-messaging` demonstrates how to publish events to a topic-based
in-process broker from `x/messaging`. A single endpoint accepts a payload and
topic name, wraps it in a `BrokerMessage`, and publishes it; no subscriber is
wired in this service — subscribers are external consumers.

Hard rules:

- `x/messaging` is the only allowed `x/*` import.
- No new third-party dependencies.
- No hidden globals, `init()` registration, or reflection routing.
- Keep `main.go` thin: load config, construct app, register routes, start.
- Keep middleware wiring explicit in `internal/app/app.go`.
- Keep all routes explicit in `internal/app/routes.go`.
- Preserve `func(http.ResponseWriter, *http.Request)` handler shape.
- Use `contract.WriteResponse` for success and `contract.WriteError` with
  `contract.NewErrorBuilder()` for errors.
- Topics must be non-empty; reject empty topic with 400 before publishing.

## 3. Package Ownership

- `main.go`: process entrypoint only.
- `internal/config`: config loading, defaults, and environment (APP_ADDR, APP_DEBUG).
- `internal/app/app.go`: middleware chain (requestid → accesslog → recovery),
  graceful shutdown.
- `internal/app/routes.go`: GET /healthz (inline), POST /events/publish.
- `internal/handler`: MessagingHandler with Publish() method.

Dependency direction:

```text
main.go → config → app → handler, x/messaging
handler → contract, router, x/messaging
```

Handlers must not import `internal/app` or `internal/config`.

## 4. Change Patterns

Add a subscriber:

1. Implement a consumer struct with a `Start(ctx context.Context)` method.
2. Subscribe to the broker topic in `Start`.
3. Start the consumer in `app.Start()` before the HTTP server.
4. Add a focused test for the consumer using a test broker.

Add a new publish endpoint:

1. Add a method to `MessagingHandler` in `internal/handler`.
2. Register the route in `routes.go`.
3. Add a focused handler test.

## 5. Validation

For docs-only changes:

```bash
git diff --check
```

For Go changes inside this service:

```bash
cd reference/with-messaging && go test -race -timeout 30s ./...
go run ./internal/checks/dependency-rules
```

## 6. Review Focus

When reviewing or optimizing this service, check:

- Empty topic names are rejected with 400 before the broker is called.
- `x/messaging` only — no other `x/*` packages.
- Response uses 202 Accepted for published events (not 200 OK).
- Response envelope uses `contract.WriteResponse` / `contract.WriteError`.
- No message payload contains credentials or secrets.
