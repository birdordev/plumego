# Scaffold And Reference Contract

`reference/standard-service` is the canonical hand-written application shape.
The `cmd/plumego new --template canonical` scaffold must stay aligned with that
shape, but it is generated as a project-local layout.

## Canonical Contract

The canonical scaffold must preserve these properties:

- `cmd/app/main.go` loads config, constructs the app, registers routes, and
  starts the server with a signal-aware context. The generated serving path
  closes the listener and waits for in-flight requests when it receives a
  shutdown signal. `APP_SHUTDOWN_TIMEOUT_MS` defaults to 5000. If the deadline
  expires, the app closes the remaining connections and reports the error.
- `internal/app/app.go` owns app construction and middleware wiring.
- `internal/app/routes.go` owns explicit route registration.
- `internal/domain/item/item.go` owns the sample item model and repository.
- The generated sample provides create, read, update, and delete routes. The
  CLI smoke test builds it, generates OpenAPI with
  `plumego generate spec --strict`, checks declared statuses against the
  routes over HTTP, and verifies readiness and signal-driven shutdown on Unix.
- The generated `plumego.spec.yaml` declares the HTTP responses. Regeneration
  preserves a customized spec. Canonical item writes use
  `contract.DecodeJSONRequest` and require a single non-null JSON object of up
  to 1 MiB. They return 400 for invalid input, 413 for oversized bodies, and
  415 for unsupported media types.
- `internal/handler/*` owns HTTP handlers and local DTOs. Generated readiness
  probes accept explicit component checkers and report a public 503 with
  stable component status when a checker fails.
- `internal/config/config.go` owns generated project config.
- No `x/*` imports appear in the canonical template.
- No hidden `init` registration, global providers, or service-locator context
  patterns appear in generated canonical code.

## Baseline Template Aliases

`minimal`, `fullstack`, and `microservice` are accepted template names that
produce the same output as `canonical`. They exist for discoverability but carry
no distinct capability profile.

## Scenario Profiles

Scenario templates keep the canonical bootstrap and add one explicit capability
profile:

| Template | Capability |
| --- | --- |
| `api`, `rest-api` | `x/rest` resource wiring |
| `tenant-api` | `x/tenant` resolution, policy, quota, and rate limit |
| `gateway` | `x/gateway` proxy and rewrite |
| `realtime` | `x/websocket` plus messaging marker |
| `ai-service` | `x/ai/provider`, `x/ai/session`, `x/ai/streaming`, `x/ai/tool` |
| `ops-service` | `x/observability` and protected `x/observability/ops` DTOs |

Scenario profiles do not promote those extensions to stable status.

## Validation

The scaffold contract is enforced by tests in
`cmd/plumego/internal/scaffold/scaffold_test.go` and by the repository
reference-layout check:

```bash
cd cmd/plumego && go test -timeout 20s ./internal/scaffold
go run ./internal/checks/reference-layout
```

The `api` and `rest-api` templates ship resource-specific OpenAPI hints.
The REST smoke test generates a strict spec, builds and starts the application,
checks real CRUD responses against the spec and waits for signal-driven HTTP
drain.
