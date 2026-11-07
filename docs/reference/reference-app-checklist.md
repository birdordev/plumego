# Reference App Checklist

This checklist defines the quality bar for every application in `reference/`.
Apply it when creating a new reference app, reviewing a contribution, or
auditing an existing one. Each section maps to a distinct concern; all items
within a section must pass before the app is considered complete.

The canonical shape lives in `reference/standard-service`. Read it first.

---

## 1. Purpose checklist

- [ ] The app demonstrates **exactly one** incremental capability on top of
  `standard-service` (or the stated baseline), not a general-purpose service.
- [ ] The chosen capability maps to a single `reference/with-<capability>`
  slot defined in `specs/task-routing.yaml`; no two reference apps cover
  identical ground.
- [ ] `standard-service` and `production-service` are not extended with
  `x/*` features; those belong in a matching `with-*` app.
- [ ] The app is a teaching tool, not a production bundle. Every wiring
  decision must be visible and intentional.
- [ ] The scope is narrow enough that a new contributor can read the app from
  `main.go` to response in under 30 minutes.

---

## 2. README checklist

Every reference app must have a `README.md` that covers the following sections
in order. Add or remove sections only where the app genuinely has nothing to
say (e.g., a benchmark has no "curl examples").

- [ ] **Purpose** — one paragraph stating what the app is and what problem it
  solves for the reader.
- [ ] **What it demonstrates** — a table or bulleted list of the specific
  patterns, packages, and decisions shown. Each entry should name a Go package,
  a file, or a pattern, not just a concept.
- [ ] **What it intentionally excludes** — a companion list of capabilities
  the reader might expect but will not find, with a pointer to the right
  reference app if one exists.
- [ ] **Directory layout** — an annotated `tree`-style block showing every
  top-level directory and key files; one-line annotations explaining each
  entry. Must match the actual layout.
- [ ] **Run commands** — exact shell commands to start the app from a clean
  checkout, including required environment variables and any optional `.env`
  setup.
- [ ] **Try-it commands** — executable `curl` examples (or `websocat`/`wscat`
  for WebSocket apps, or equivalent tooling for gRPC/RPC apps) covering the
  happy path, at least one structured error response, and any protocol-specific
  operation (upgrade, stream, subscription). Commands must work against the
  unmodified app with only the documented prerequisites satisfied.
- [ ] **Configuration notes** — a table of every environment variable the app
  reads, its default, and a one-line description. Reference `env.example`.
- [ ] **Tests** — the exact command to run the test suite and a description of
  what scenarios the tests cover.
- [ ] **How to extend** — step-by-step instructions for the most common
  extension operations (add an endpoint, add a resource, enable a feature flag,
  swap in-memory storage for a real backend). Each step should be actionable
  without reading any other file.
- [ ] **Production notes** — a section (or reference to `PRODUCTION_CHECKLIST.md`)
  listing concrete changes required before promoting the app to production:
  secrets, auth, TLS, rate limiting, observability gating, timeout tuning.
- [ ] **Related references** — links to prerequisite reference apps, relevant
  `docs/modules/` primers, and the extension's `module.yaml` when applicable.

---

## 3. Code checklist

- [ ] **Explicit wiring only** — every middleware, route, and dependency is
  declared in source code at a named call site. No import-side-effect
  registration, no auto-discovery, no reflection-based routing.
- [ ] **`net/http` compatibility** — handlers use the canonical
  `func(w http.ResponseWriter, r *http.Request)` signature. No custom handler
  types, no context service-locator, no framework-owned request structs.
- [ ] **No hidden global state** — no package-level mutable variables
  initialized at startup, no `sync.Once`-hidden singletons, no shared state
  outside explicitly constructed types.
- [ ] **No `init()` registration** — packages must not register routes,
  middleware, or services in `init()`. All side-effect-free defaults are
  acceptable; wiring is not.
- [ ] **No ad hoc response envelopes** — all success writes go through
  `contract.WriteResponse`; all error writes go through `contract.WriteError`
  with `contract.NewErrorBuilder()`. No per-handler JSON structs that invent a
  new envelope shape.
- [ ] **No unrelated dependencies** — the app's `go.mod` lists only
  `github.com/spcent/plumego` (via `replace` directive) and the one external
  package the scenario specifically demonstrates. A dependency that is
  incidental to the demo (ORM, utility library, logging framework) must not
  appear. Document the rationale in `go.mod` or `README.md` when a non-obvious
  external dep is required.
- [ ] **No broad framework magic** — no DI container, no controller scanning,
  no annotation-driven route generation, no ORM magic. Every data flow is
  traceable by reading sequential function calls.
- [ ] **Application layout matches the canonical shape** — `main.go` is
  process-entrypoint only (load config → construct app → register routes →
  start); `internal/config/` owns config loading; `internal/app/app.go` owns
  middleware wiring; `internal/app/routes.go` owns the route table;
  `internal/handler/` owns HTTP adaptation; `internal/domain/` owns business
  logic.
- [ ] **Dependency direction is one-way** — `main.go` → `internal/config` →
  `internal/app` → `internal/handler` → `internal/domain`. `internal/handler`
  must not import `internal/app` or `internal/config`.
- [ ] **Middleware is transport-only** — no middleware builds DTOs, injects
  services, or makes domain-policy decisions.
- [ ] **Tests next to behavior** — handler tests live in `internal/handler/`,
  app-level integration tests live in `internal/app/`. Domain tests live next
  to the domain package they test. No separate top-level `test/` directory.
- [ ] **`x/*` imports are scoped to the demonstrated capability** — the
  `with-*` apps import exactly the one `x/*` family they showcase and nothing
  else. `standard-service` and `production-service` import no `x/*` packages.

---

## 4. Test checklist

- [ ] Tests cover the handler's happy path at the HTTP level via `httptest`.
- [ ] Tests cover at least one structured error path (invalid input, missing
  field, not found) and assert the response envelope shape.
- [ ] Domain logic is tested independently from HTTP wiring via interface stubs
  or in-memory implementations.
- [ ] Middleware ordering is visible in at least one test (e.g., verifying
  that `request_id` appears in the error response).
- [ ] Tests pass with the race detector: `go test -race ./...`.
- [ ] Tests do not rely on a live network, database, or external process
  unless the scenario explicitly demonstrates integration with one (in which
  case the test is skipped when the external resource is absent and the
  README documents the prerequisite).
- [ ] No global test state; each test sets up and tears down its own fixtures.
- [ ] Coverage over the demonstrated capability is sufficient to detect
  obvious regressions; aim for all exported handler methods and at least one
  route-registration check.

---

## 5. Validation checklist

Run these in order. Stop at the first failure and fix it before proceeding.

### Per-app validation

```bash
# Run the app's own tests with the race detector
cd reference/<name> && go test -race -timeout 30s ./...
```

```bash
# Verify the app compiles and starts (where practical — skip for one-shot demos)
cd reference/<name> && go run . &
sleep 2 && curl -sf http://localhost:<port>/healthz
kill %1
```

### Repo-wide boundary checks

```bash
# Boundary and layout checks (always run after any reference-app change)
go run ./internal/checks/dependency-rules
go run ./internal/checks/reference-layout
go run ./internal/checks/module-manifests
```

### Diff gate (preferred for focused changes)

```bash
make validate-diff
```

### Reference suite (run when multiple apps changed or layout rules updated)

```bash
# Vet every standalone reference module
make reference-vet

# Test every standalone reference module
make reference-test
```

### Full CI gate (run before release or when gate profile requires it)

```bash
make gates
```

> **When to run `make gates`:** required when the change touches stable-root
> packages, extension maturity, public API, or the `website/src/generated/`
> staleness check. Docs-only changes to `docs/reference/` do not require the
> full gate suite; `make validate-diff` is sufficient.

---

## 6. Scope-control checklist

- [ ] The app changes only files inside its own `reference/<name>/` directory.
  Changes to stable-root packages, extensions, or other reference apps require
  a separate PR.
- [ ] No new top-level roots are introduced (`ai`, `utils`, `net`, `rest`,
  `validator`, `pubsub`, `frontend`, or similar catch-all names).
- [ ] The app does not import another `reference/*` module.
- [ ] If the scenario requires a capability not yet in a stable root or `x/*`
  extension, the capability is built into `internal/` within the reference app,
  not hoisted into the main module.
- [ ] `go.mod` uses a `replace` directive pointing at the repo root; the
  rationale is documented (teaching tool, showcases feature X with external dep
  Y) following the pattern established by `with-rest`, `with-rpc`, and
  `benchmark` (the current reference apps with external dependencies).
- [ ] No deprecated stable-root symbols are used. Callers must use the
  replacement documented in `specs/deprecation-inventory.yaml`.

---

## 7. Production notes checklist

Every reference app that can be adapted for production use must include a
`PRODUCTION_CHECKLIST.md` (or a clearly labelled section in `README.md`) that
addresses the following items. Items that are not applicable for a particular
scenario (e.g., no auth in a benchmark) must be listed as "N/A" with a
one-line reason.

- [ ] **Secrets and credentials** — list every secret the app reads from the
  environment, state the minimum length or entropy requirement, and document
  the startup behaviour when the secret is absent or too short (error, warning,
  default).
- [ ] **Authentication and authorisation** — state whether the app ships with
  auth disabled for demo purposes, the exact config change needed to enable it,
  and the expected behaviour after enabling.
- [ ] **TLS** — state how to enable TLS (`core.AppConfig.TLS`) and any proxy
  header considerations.
- [ ] **CORS** — document the demo default (wildcard `*` or open), the
  production override (`APP_CORS_ALLOWED_ORIGINS`), and the startup warning
  emitted when the production value is absent.
- [ ] **Timeouts** — list `ReadTimeout`, `WriteTimeout`, `IdleTimeout` and
  their demo-default values, with guidance for production tightening.
- [ ] **Rate limiting and abuse guards** — state whether rate limiting is
  included; if not, point to `middleware/abuseguard` and `with-ops`.
- [ ] **Observability** — describe what observability the demo provides (noop
  collector, in-process spans) and the steps to wire a real Prometheus
  collector or OTLP exporter.
- [ ] **In-memory state** — call out any demo-only in-memory stores, state
  that they are not durable, and provide the interface or pattern to swap in a
  real backend.
- [ ] **Removing demo-only routes** — list any routes that must be removed or
  gated before production (e.g., `/api/v1/spans`, unauthenticated `/metrics`).
- [ ] **Container build** — if a `Dockerfile` is present, document the build
  context requirement (repo root due to the `replace` directive) and any
  required `--build-arg` values.

---

## 8. Agent execution checklist

Use this section when an AI agent creates or modifies a reference app.

### Before editing

- [ ] Read `AGENTS.md` (repo root) and the matching `specs/task-routing.yaml`
  entry for the task type (`app_bootstrap`, `http_endpoint`, or `websocket`).
- [ ] Read `docs/reference/canonical-style-guide.md`.
- [ ] Read `reference/standard-service` as the canonical baseline.
- [ ] Read the target app's `module.yaml` if one exists; otherwise treat
  `go.mod` as the module boundary.
- [ ] State the preflight: context package, owning module, in-scope paths,
  out-of-scope paths, public API / dependency / behavior / security / docs
  impact (none|yes), and validation plan.

### During editing

- [ ] One primary module per change. Do not opportunistically refactor
  unrelated files.
- [ ] Preserve the existing public contract (handler signatures, route paths,
  response envelope shape, error codes) unless the task explicitly asks for a
  change.
- [ ] Add or update tests next to each changed behavior.
- [ ] Do not add new `x/*` imports beyond the one package the scenario
  demonstrates.
- [ ] Do not introduce new third-party dependencies without explicit approval.

### After editing

- [ ] Run the per-app test suite:
  ```bash
  cd reference/<name> && go test -race -timeout 30s ./...
  ```
- [ ] Run the diff gate:
  ```bash
  make validate-diff
  ```
- [ ] If layout rules, module manifests, or dependency rules were touched, run
  boundary checks explicitly:
  ```bash
  go run ./internal/checks/dependency-rules
  go run ./internal/checks/reference-layout
  go run ./internal/checks/module-manifests
  ```
- [ ] Confirm the README covers all sections in the README checklist (§2).
- [ ] Confirm that every curl / WebSocket / RPC try-it command in the README
  works against the unmodified app.
- [ ] Summarise validation results in the PR description as:
  `<command> → <status> | <first failure if any> | <next step>`.

---

## Cross-references

- Canonical app layout and style rules: `docs/reference/canonical-style-guide.md`
- Reference app index and scenario map: `reference/README.md`
- Extension maturity and beta/experimental status: `docs/concepts/extension-maturity.md`
- Boundary rules: `specs/dependency-rules.yaml`
- Task routing and context packages: `specs/task-routing.yaml`
- Gate profiles: `docs/operations/agent-code-quality-rules.md §6`
- Deprecation inventory: `specs/deprecation-inventory.yaml`
