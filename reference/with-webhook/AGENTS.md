# AGENTS.md - reference/with-webhook

Operational guide for agents working in `reference/with-webhook`.

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
and extends it with inbound webhook reception.

## 2. Purpose And Boundaries

`reference/with-webhook` demonstrates inbound webhook reception using
`x/messaging/webhook`. Webhook endpoints are auto-registered by
`webhook.Inbound.RegisterRoutes()`; received payloads are published to an
in-process broker. No explicit HTTP handlers are needed for the webhook routes.

Hard rules:

- `x/messaging/webhook` and `x/messaging/pubsub` are the only allowed `x/*` imports.
- No new third-party dependencies.
- No hidden globals, `init()` registration, or reflection routing.
- Keep `main.go` thin: load config, construct app, register routes, start.
- Keep middleware wiring explicit in `internal/app/app.go`.
- Webhook routes are auto-registered via `webhook.Inbound.RegisterRoutes()` —
  this is the intended pattern, not a violation of explicit routing.
- Use `contract.WriteResponse` / `contract.WriteError` for any directly-served
  responses (health, error pages).
- Pass the caller's signal context to the shared HTTP runtime to drain inbound requests.

## 3. Package Ownership

- `main.go`: process entrypoint only.
- `internal/config`: config loading, defaults, and environment (APP_ADDR, APP_DEBUG,
  APP_ENV_FILE).
- `internal/app/app.go`: middleware chain (requestid → accesslog → recovery),
  HTTP serving and graceful shutdown.
- `internal/app/routes.go`: GET /healthz (inline), webhook route registration.

Dependency direction:

```text
main.go → config → app → x/messaging/webhook, x/messaging/pubsub
```

## 4. Change Patterns

Add a webhook provider:

1. Configure a new provider entry in `webhook.Inbound` options in `routes.go`.
2. Set the provider's shared secret in config and document it in `env.example`.
3. Add a test that sends a signed request and asserts the 200 response and broker publish.

Subscribe to received webhook events:

1. Implement a consumer that subscribes to the broker topic for the webhook provider.
2. Start the consumer in `app.Start()`.
3. Add a focused test for the consumer.

Add a health probe for a webhook dependency:

1. Implement `health.ComponentChecker` and register it before `Prepare()`.
2. The `/readyz` route reflects registered checkers automatically.

## 5. Validation

For docs-only changes:

```bash
git diff --check
```

For Go changes inside this service:

```bash
cd reference/with-webhook && go test -race -timeout 30s ./...
go run ./internal/checks/dependency-rules
```

## 6. Review Focus

When reviewing or optimizing this service, check:

- `x/messaging/webhook` and `x/messaging/pubsub` only — no other `x/*` packages.
- Webhook secret validation: `AllowUnauthenticated: true` is acceptable for the demo
  but must be replaced with real secret validation before production use.
- Graceful shutdown lets in-flight inbound HTTP requests finish within the drain budget.
- No webhook payload is logged in full — avoid logging credentials or PII from
  third-party webhooks.
- Response envelope uses `contract.WriteResponse` / `contract.WriteError` for
  directly-served responses.
