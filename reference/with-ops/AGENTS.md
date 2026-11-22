# AGENTS.md - reference/with-ops

Operational guide for agents working in `reference/with-ops`.

This file is intentionally short. It compresses the local rules agents need for
routine changes in this feature demo. The repository root `AGENTS.md` remains
authoritative; when a task becomes architectural, security-sensitive, or
cross-module, fall back to the root workflow and the matching
`specs/task-routing.yaml` entry.

## 1. Minimal Context

For changes confined to this directory, load only:

- repository root `AGENTS.md`, if not already loaded
- this file
- the touched Go files and their tests

Read these local docs only when they answer a concrete question:

- `ARCHITECTURE.md`: layout, middleware, and HTTP serving
- `AGENT_TASKS.md`: adding hooks, changing the ops token, adding adjacent routes
- `PRODUCTION_CHECKLIST.md`: ops token security, network restriction, shutdown

## 2. Purpose And Boundaries

`reference/with-ops` demonstrates how to mount protected `x/observability/ops`
routes without promoting debug tooling to production defaults. It passes
`core.App` as the ops route registrar.

Hard rules:

- No `x/*` imports beyond `x/observability` and `x/observability/ops`.
- No domain models or business logic.
- Do not mount `x/observability/devtools`.
- Do not expose `/ops/*` routes without `ops.AuthConfig` token enforcement.
- Do not hard-code the ops token in source; read it from the environment.
- Use the signal context and the shared HTTP runtime for shutdown.

## 3. Package Ownership

- `main.go`: config loading, app construction, route registration, and signals.
- `internal/app`: middleware, ops routes, and HTTP serving.
- `internal/handler`: the application and metrics endpoints.

## 4. Validation

For docs-only changes:

```bash
git diff --check
```

For Go changes inside this service:

```bash
go test -C reference/with-ops -race -timeout 30s ./...
go run ./internal/checks/dependency-rules
go run ./internal/checks/module-manifests
```

## 5. Review Focus

When reviewing or optimizing this service, check:

- ops token is read from environment, never hard-coded
- `/ops/*` routes remain token-gated — no unauthenticated access
- `x/observability/devtools` is not mounted
- metrics and access logs observe the recovered status (`requestid → httpmetrics → accesslog → recovery`)
- no domain logic has been added to `main.go`
