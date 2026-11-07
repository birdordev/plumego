# Plumego Canonical Style Guide

Scope: `core`, `router`, `middleware`, official docs, code generation, AI-agent workflows.

---

## 1. Core Principles

- **stdlib first** — stay close to `http.Handler`, `*http.Request`, `http.ResponseWriter`, `httptest`
- **one obvious way** — one bootstrap, one route style, one handler shape, one decode path, one error shape, one test style
- **explicit over implicit** — no hidden binding, no context service-locator, no magical response wrappers, no import-order behavior
- **small-step refactorability** — narrow boundaries, stable interfaces, shallow call paths, minimal indirection
- **single canonical path** — the reference app defines structure

When convenience conflicts with predictability, choose predictability.

---

## 2. Package Roles

### `core`
App construction, lifecycle, route registration entry points, middleware attachment, server startup.
Must stay a kernel and must not become a feature catalog or generic plugin container.

### `router`
Route matching, params, grouping, route tree/lookup, static mounting.
Not allowed: repositories, validators, JSON writers, business response wrappers, service construction.

### `middleware`
Transport-layer cross-cutting only: logging, recovery, timeout, request-id, CORS, auth adapters, rate-limiting, tracing, metrics.
Not allowed: service injection, ORM lookup, business DTO assembly, hidden request binding, domain-policy branching.
Prefer narrow packages such as `requestid`, `tracing`, `accesslog`, `recovery`, `timeout`.

### Extension packages
`x/ai`, `x/observability/ops`, `x/tenant`, `x/websocket`, `x/messaging/webhook`, `x/messaging/scheduler`, and sibling `x/*` packages are capability layers, not the core learning path. They must not define the primary coding style.

### Reference and Templates
`reference/standard-service` is the only canonical application layout.

---

## 3. Application Structure

```text
cmd/myservice/main.go
internal/config/config.go
internal/app/app.go
internal/app/routes.go
internal/handler/health.go
internal/handler/user_create.go
internal/handler/handler_test.go
internal/domain/user/service.go
internal/domain/user/repository.go
```

This layout matches `reference/standard-service` and must be copied by templates and scaffolds, not reinvented per example.

- `cmd/` — startup only (load config, construct app, start server)
- `internal/config/` — config loading, defaults, validation
- `internal/app/` — HTTP wiring: core construction, middleware order, route table
- `internal/handler/` — HTTP adaptation: one file per handler group, tests alongside
- `internal/domain/` — business logic and domain models
- `internal/platform/` — optional app-local infra adapters only when the behavior does not already belong to a stable Plumego package
- success and error writes should go directly through `contract.WriteResponse` / `contract.WriteError` from handlers instead of app-local JSON/error helper families
- Do not mix routing, domain logic, persistence, and transport helper policy in one package

---

## 4. Canonical Bootstrap

```go
func main() {
    cfg := core.DefaultConfig()
    app := core.New(cfg, core.AppDependencies{})

    if err := app.Use(RequestID(), Recovery(), RequestLogger()); err != nil {
        log.Fatal(err)
    }

    if err := registerRoutes(app); err != nil {
        log.Fatal(err)
    }

    if err := app.Prepare(); err != nil {
        log.Fatal(err)
    }
    srv, err := app.Server()
    if err != nil {
        log.Fatal(err)
    }
    defer func() {
        if err := app.Shutdown(context.Background()); err != nil {
            log.Printf("shutdown server: %v", err)
        }
    }()

    if err := srv.ListenAndServe(); err != nil {
        log.Fatal(err)
    }
}
```

Rules:
- One visible construction site
- Global middleware attached explicitly near startup
- One `registerRoutes` call per bounded area
- No `init()` side-effect registration, no hidden auto-registration

---

## 5. Route Registration

```go
func registerRoutes(app *core.App) error {
    if err := app.Get("/healthz", healthHandler); err != nil {
        return err
    }
    if err := app.Post("/users", createUserHandler); err != nil {
        return err
    }
    return nil
}
```

- One method + one path + one handler per line
- Route registration errors must be returned to the caller; do not log and continue
- Use `app.Any(...)` for catch-all method registration; do not introduce overlapping helper aliases for the same ANY-route behavior
- Use `app.AddRoute(..., router.WithRouteName(...))` for named routes; keep one canonical metadata path instead of named-route helper aliases
- Static registration; no reflection or discovery
- Must remain grep-friendly — path and handler discoverable by search
- Groups: path prefix and shared middleware only; not for hidden policy or service injection

### Error-accumulating registration (canonical for multi-route tables)

For large route tables, the per-line `if err != nil { return err }` form buries the route map. Use a small local `routeReg` accumulator that wraps a `routeAdder` interface and retains the first error, exposing lowercase `get`/`post`/… forwarders — keeping one method + one path + one handler per line while still returning the first registration error to the caller. This is a **sanctioned canonical pattern** — no reflection, discovery, or hidden policy. See `reference/standard-service/internal/app/routes.go` for the canonical implementation; copy it, don't reinvent.

---

## 6. Handler Style

Canonical signature:

```go
func(w http.ResponseWriter, r *http.Request)
```

Handler responsibilities (transport only):
1. Read request inputs
2. Validate transport-level shape
3. Call a service
4. Translate outcome to transport response

Not in handlers: raw SQL, repo construction, context service-lookup, config loading, transaction orchestration, response envelope invention.

---

## 7. Request Decoding

```go
type CreateUserRequest struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
    var req CreateUserRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        _ = contract.WriteError(w, r, contract.NewErrorBuilder().
            Type(contract.TypeValidation).
            Code("invalid_json").
            Message("invalid request body").
            Build())
        return
    }
    // transport-level validation here
}
```

Thin helper `DecodeJSON(r, &req)` acceptable if: reads exactly one source, returns plain Go errors, no hidden side effects.

Not canonical: middleware-first binding into context, mixed-source auto-binding, magic key DTO retrieval.

---

## 8. Parameter Access

```go
// Route param
id := Param(r, "id")
if id == "" {
    _ = contract.WriteError(w, r, contract.NewErrorBuilder().
        Type(contract.TypeRequired).
        Code("missing_id").
        Message("missing route parameter id").
        Build())
    return
}

// Query param
r.URL.Query().Get("page")

// Header
r.Header.Get("Authorization")
```

Data source must be visible at the read site — reader must see whether value came from path, query, header, or body.

---

## 9. Middleware Style

```go
type Middleware func(http.Handler) http.Handler
```

`next` must be called exactly once. No pre/post/error lifecycle splits for ordinary middleware.

Not canonical: business DTO construction, service injection into context, domain success/failure decisions, mutating domain semantics.

### Middleware Constructors

Canonical constructor shape depends on whether construction can fail:

- If construction cannot fail, expose `Middleware(...) middleware.Middleware`.
- If dependencies or config can be invalid, expose `MiddlewareE(...) (middleware.Middleware, error)` and make new call sites handle the error.
- Existing panic wrappers such as `Middleware(...)` or `Recovery(...)` may remain only as documented compatibility paths when a safe `*E` constructor exists.
- Do not add new panic-only middleware constructors.

---

## 10. Error Model

```go
type errorPayload struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

// One write path:
contract.WriteError(w, r, contract.NewErrorBuilder().
    Type(contract.TypeValidation).
    Code(contract.CodeInvalidJSON).
    Message("invalid request body").
    Build())
```

Rules:
- Structured errors only (no ad hoc `http.Error` in JSON APIs)
- Explicit or predictably derived status codes
- Stable machine-readable error codes
- Logs may be richer; responses must not leak internals
- Identical error class → identical shape across modules

---

## 11. Success Responses

```go
contract.WriteResponse(w, r, http.StatusCreated, CreateUserResponse{ID: id}, nil)
```

- One response helper, used consistently
- No envelope proliferation (`{ "success": true, "data": ... }` etc.) unless there is one global envelope documented once and used everywhere
- Meaningful HTTP status codes set explicitly

---

## 12. Dependency Injection

```go
type UserHandler struct {
    Service user.Service
}

func (h UserHandler) Create(w http.ResponseWriter, r *http.Request) { ... }
```

Not canonical:
```go
svc := MustServiceFromContext(r.Context(), "userService")  // obscures shape, fragile
```

Route wiring file must make clear: who constructs the handler, what its dependencies are, which routes use it.

---

## 13. Testing Style

```go
func TestHealth(t *testing.T) {
    app := core.New(core.DefaultConfig(), core.AppDependencies{})
    if err := app.Get("/healthz", healthHandler); err != nil {
        t.Fatal(err)
    }

    req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    rec := httptest.NewRecorder()
    app.ServeHTTP(rec, req)

    if rec.Code != http.StatusOK {
        t.Fatalf("expected 200, got %d", rec.Code)
    }
}
```

Rules:
- `httptest` first
- Table-driven for pure transport cases
- Domain mocks behind interfaces
- Middleware order visible in the test file
- No full-framework bootstrap for simple route tests
- No hidden global state between tests

---

## 14. AI Agent Rules

Tasks must state: goal, in-scope paths, out-of-scope paths, owning module, API/dependency policy, required tests, validation commands, done definition.

Ambiguous/broad tasks: analysis-only pass first; split into small reversible cards; no edits until scope is explicit.

Exported symbol changes: enumerate callers with `rg`, migrate all in one change, verify zero residual references.

Reviews: findings first, no code changes unless requested; prioritize boundaries, regressions, concurrency, missing tests.

Default: prefer stdlib; follow this guide over convenience APIs; preserve canonical patterns; treat `utils`/`validator`/`rest`/`pubsub`/`tenant` as migration debt.

## 16. Agent-First Repo Rules

Start from `reference/standard-service`. Center each change on one primary module. Prefer a new focused extension package over widening a stable root. Keep scaffolds, docs, and the reference app synchronized. Avoid hidden context-based dependency flow and unjustified wrapper abstractions. See §21 for the full forbidden-patterns list.

---

## 17. `contract` Package Rules

`contract` owns **transport primitives only**: request/response envelopes, error
types, HTTP writing helpers, context key accessors, and binding helpers.

### 17.1 Scope boundary

The following categories **do not belong** in `contract` and must live in `x/*`:

| Concern | Correct home |
|---|---|
| Tracing infrastructure (Tracer, Span, Collector, Sampler) | `x/observability` |
| Session lifecycle (SessionStore, SessionValidator, RefreshManager) | `x/tenant` or `x/security` |
| Metrics collection | `x/observability` |
| Business-domain validation rules | caller / domain package |

`contract` may export context keys and ID accessor functions for tracing and
auth, but must not own the full instrumentation or session management subsystem.

### 17.2 Context accessor naming

All context accessor pairs in `contract` use the **With/From** pattern:

```go
// correct
func WithFoo(ctx context.Context, v Foo) context.Context
func FooFromContext(ctx context.Context) Foo

// wrong — mixed prefix order
func ContextWithFoo(ctx context.Context, v Foo) context.Context  // ← forbidden
func FooFromContext(ctx context.Context) Foo
```

Context key types are always **unexported** zero-value structs and always
**inlined** at the call site — no package-level variable:

```go
type fooContextKey struct{}

// correct
context.WithValue(ctx, fooContextKey{}, v)

// wrong
var fooKey fooContextKey            // ← unnecessary variable
context.WithValue(ctx, fooKey, v)
```

### 17.3 Error construction path

One canonical path:

```go
err := contract.NewErrorBuilder().
    Type(contract.TypeValidation).
    Message("validation failed").
    Build()

_ = contract.WriteError(w, r, err)
```

Use `Type(...)` when a known `ErrorType` exists, then override `Code`, `Status`,
or `Category` only when the transport contract truly needs a deviation.

**Do not add** `Ctx.ErrorJSON`, `HandleError`, `SafeExecute`, or new
`NewXxxError(...)` convenience layers on top of this path.

### 17.4 Success response path

One canonical path:

```go
// preferred
contract.WriteResponse(w, r, http.StatusOK, data, meta)
```

`WriteJSON` is a low-level raw-payload writer. It is not the success path.
Use `WriteResponse` from stdlib-shaped handlers.

Do not invent per-feature envelope shapes.

### 17.5 Deprecated API policy

When replacing an API in `contract`:

1. Mark the old symbol: `// Deprecated: Use Foo instead.` (first line of doc comment).
2. **Do not** keep it as a wrapper for more than one minor release.
3. Replace all callers in the same PR that introduces the new API, then delete
   the old symbol — zero dead wrappers at merge time.

Exception: if external callers exist and cannot be migrated in one PR, document
the deprecation deadline in the commit message and a task card.

Change strategy:
1. Keep public behavior unchanged
2. Align local code to canonical style
3. Extract helpers only when repetition justifies it
4. Do not refactor unrelated files opportunistically

Conflict rule: if existing code conflicts with this guide — preserve behavior first, avoid mixing styles in the touched area, move toward canonical when diff is clear and bounded.

---

## 18. Compatibility APIs

- Must not appear in canonical docs
- Must be labeled clearly
- New features must not build on compatibility-only surfaces
- Migration always moves toward canonical, never away
- Any retained compatibility, alias, deprecated, unsupported-operation, or TODO
  marker must be registered in `specs/deprecation-inventory.yaml` with
  `category`, `status`, `owner`, `decision`, `replacement`, and `paths`.
  `status: decision_required` is not allowed for v1 release readiness.

---

## 19. Review Checklist

- Request flow obvious from route to response?
- Code stays close to `net/http` semantics?
- Single visible source for each input?
- JSON decoding explicit or via one transparent helper?
- Middleware transport-only?
- Dependencies explicit in structs or constructors?
- Response and error shapes consistent with framework conventions?
- A new contributor can trace control flow in a few minutes?

---

## 20. Canonical Examples

See `reference/standard-service` (`internal/handler/`, `internal/app/routes.go`, `internal/handler/handler_test.go`) for the canonical create-endpoint, route-wiring, and test implementations.

---

## 21. Forbidden Patterns (Canonical Code)

- Mixing `Get`, `GetCtx`, `GetHandler` styles in one example
- Binding request DTOs in middleware, reading from context in CRUD handlers
- Using `router` for repositories or resource controllers
- Retrieving services from request context maps
- Exposing platform capabilities through `core.App` when ordinary composition suffices
- Introducing new response helper families for a single feature
- Teaching multiple equally valid bootstraps in first-party docs
- Hiding route registration behind import side effects
- Placing business logic in middleware
- Blurring transport and domain package boundaries
- Using official examples to showcase compatibility-only APIs

---

## 22. Final Rule

If a reviewer cannot understand how a request is handled within a few minutes by reading only the route registration, middleware, and handler file — the code is not canonical enough.

Plumego's strength is one stable, explicit, stdlib-aligned style, not many styles.

---

## 23. Constructor Pattern Convergence

New constructor work follows the same predictability rule as route registration:
construction errors must be visible to the caller when invalid config or missing
dependencies can occur.

Canonical classifications:

- `New(...) *T` is canonical only when construction cannot fail or all defaults
  are explicit and safe, such as `core.New`, `router.NewRouter`, or simple value
  helpers.
- `New(...) (*T, error)` is canonical when config, external dependencies, file
  paths, network addresses, or storage handles can be invalid.
- `NewE(...) (*T, error)` is the safe path for packages that already shipped a
  panic-compatible `New(...) *T` wrapper. New dynamic wiring should call the
  `*E` variant.
- Family-specific safe constructors such as `NewGatewayE` are acceptable as
  app-facing extension entrypoints when they reduce discovery ambiguity.
- Panic convenience wrappers are legacy-compatible, not the default for new API
  design. Keeping one requires local docs or deprecation-inventory coverage and
  a safe constructor beside it.

Current inventory:

| Pattern | Example | Classification |
|---|---|---|
| Cannot-fail `New` | `core.New`, `router.NewRouter` | canonical |
| Error-returning `New` | `store/kv.NewKVStore`, `x/data/rw.New` | canonical — prefer for new fallible constructors |
| `New` + `NewE` pair | `x/gateway.New/NewE`, `x/messaging/mq.NewInProcBroker/NewInProcBrokerE` | legacy-compatible — migrate only in module-owned cards |
| Extension family alias | `x/gateway.NewGateway/NewGatewayE` | app-facing compatibility |
| Fallible middleware | `middleware/accesslog.Middleware(Config)` | canonical — return `(..., error)` when construction can fail |

Do not run repo-wide constructor renames as drive-by cleanup; each migration must name the owning module, API compatibility policy, docs impact, and tests.

---

## 24. Context Handling

Library functions that accept a `context.Context` must pass it through to
blocking work and never silently substitute a fresh background context.

This repo treats a **nil `context.Context` as `context.Background()`** at
package entry points (the one deviation from the standard library, which
panics on nil contexts). The rule is applied deliberately and consistently so
library code is callable from `main` without ceremony:

```go
if ctx == nil {
    ctx = context.Background()
}
```

Where this appears, keep the check at the entry point only — do not thread
nil-tolerant checks into internal helpers. New exported functions should
document that nil is accepted, and internal helpers should assume a non-nil
context.

Context values flow **down** (caller to callee); cancellation and deadlines
must not be derived from `context.Background()` inside library code. When an
operation genuinely cannot be canceled, prefer taking the context anyway and
documenting the limitation over hard-coding `context.Background()`.
