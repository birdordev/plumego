# AGENTS.md - reference/with-gateway

Operational guide for agents working in `reference/with-gateway`.

This file is intentionally short. The repository root `AGENTS.md` remains
authoritative; when a task becomes architectural, security-sensitive, or
cross-module, fall back to the root workflow and the matching
`specs/task-routing.yaml` entry.

## 1. Minimal Context

For changes confined to this directory, load only:

- repository root `AGENTS.md`, if not already loaded
- this file
- the touched Go files and their tests

Read `reference/standard-service` first — this service has the same core bootstrap
shape and extends it with a reverse proxy.

## 2. Purpose And Boundaries

`reference/with-gateway` demonstrates a minimal reverse proxy that forwards all
matched requests to a configurable backend URL using `x/gateway`. Health probes
for the proxy itself are registered separately. Configure `GATEWAY_BACKEND` to
point at the upstream service.

Hard rules:

- `x/gateway` is the only allowed `x/*` import.
- No new third-party dependencies.
- No hidden globals, `init()` registration, or reflection routing.
- Keep `main.go` thin: load config, construct app, register routes, start.
- Keep middleware wiring explicit in `internal/app/app.go`.
- Keep all routes explicit in `internal/app/routes.go`.
- Preserve `func(http.ResponseWriter, *http.Request)` handler shape.
- Use `contract.WriteResponse` / `contract.WriteError` for any directly-served
  responses (health, error pages); proxy responses pass through unchanged.
- `GATEWAY_BACKEND` must be non-empty and validated at startup.

## 3. Package Ownership

- `main.go`: process entrypoint only.
- `internal/config`: config loading, defaults, flags, environment (GATEWAY_BACKEND,
  APP_DEBUG, APP_ENV_FILE).
- `internal/app/app.go`: middleware chain (requestid → security → accesslog →
  recovery → bodylimit → timeout), graceful shutdown.
- `internal/app/routes.go`: health routes, then the proxy catch-all.
- `internal/handler`: HealthHandler (liveness/readiness probes for the proxy itself).

Dependency direction:

```text
main.go → config → app → handler, x/gateway
handler → contract, router
```

Handlers must not import `internal/app` or `internal/config`.

## 4. Change Patterns

Change the backend URL:

1. Set `GATEWAY_BACKEND` in `.env` or the process environment.
2. No code change needed — the proxy reads this from config at startup.

Add per-route proxy rules (path rewriting, header injection):

1. Configure the `x/gateway` proxy with the desired transform options in `routes.go`.
2. Register a named route instead of the catch-all when path-specific rules apply.
3. Add a test that stubs the backend with `httptest.NewServer`.

Add a health check dependency:

1. Implement `health.ComponentChecker` for the backend.
2. Register it with the core app before `Prepare()`.
3. The `/readyz` route automatically reflects all registered checkers.

## 5. Validation

For docs-only changes:

```bash
git diff --check
```

For Go changes inside this service:

```bash
cd reference/with-gateway && go test -race -timeout 30s ./...
go run ./internal/checks/dependency-rules
```

## 6. Review Focus

When reviewing or optimizing this service, check:

- `GATEWAY_BACKEND` is validated as non-empty at startup; the proxy must not start
  with an empty backend URL.
- `x/gateway` only — no other `x/*` packages.
- CORS headers from the backend pass through unchanged; the proxy must not
  impose its own CORS policy unless explicitly required.
- Response envelope uses `contract.WriteResponse` / `contract.WriteError` only for
  directly-served responses (health, validation errors).
- Timeout middleware wraps proxy requests so slow backends do not hang connections.
