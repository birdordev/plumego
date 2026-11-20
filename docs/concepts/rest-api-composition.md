# Composing `x/rest`, `x/validate`, and `x/openapi`

These three extensions cover adjacent concerns for building a JSON resource API,
but they are **intentionally decoupled** — none imports another. A developer
wires them together at the application layer. This guide shows how the pieces
fit and which one owns what, so you do not reach for the wrong tool or expect a
coupling that does not exist.

## Who owns what

| Concern | Owner | Does NOT own |
|---|---|---|
| CRUD routing, pagination, query parsing, list/member handlers | `x/rest` | Request validation, doc generation |
| Decoding a request body and running validation on it | `x/validate` | Routing, persistence, the validator implementation itself |
| Generating an OpenAPI 3.1 document from registered routes | `x/openapi` | Routing, validation, serving the spec |
| Success/error response envelope | `contract` (used by `x/rest`) | — |

Key consequence: there is **no single "REST resource with validation and docs"
constructor**. You compose three independent calls. That is by design — each
extension stays usable on its own.

## The composition, end to end

### 1. Resource routing — `x/rest`

`x/rest` registers the canonical CRUD route set for a resource from a spec plus
a repository. Responses go through `contract.WriteResponse` / `contract.WriteError`,
so the envelope matches the rest of the service.

```go
spec := rest.DefaultResourceSpec("users").WithPrefix("/api/users")
users := rest.NewDBResource[user.User](spec, user.NewRepository())
if err := rest.RegisterResourceRoutes(r, spec.Prefix, users, spec.Routes); err != nil {
    return err
}
```

`user.Repository` only has to satisfy `rest.Repository[T]` (`FindAll`, `Count`,
…). `x/rest` does not validate inputs — that is the next layer.

### 2. Request validation — `x/validate`

For handlers that accept a body (typically Create/Update, or any custom write
handler), decode and validate in one step with `validate.Bind`. `x/validate`
deliberately does **not** ship a validator; you pass any value that satisfies
the one-method `validate.Validator` interface:

```go
// type Validator interface { Validate(v any) error }

dto, err := validate.Bind[CreateUserRequest](r, v) // v is your Validator
if err != nil {
    // err is a *validate.ValidationError carrying a contract APIError shape
    contract.WriteError(w, r, /* … */)
    return
}
```

`validate.BindJSON[T]` is the variant when you only need JSON decoding with no
validation. Malformed JSON is reported before validation runs, so a body that
cannot be decoded never reaches `Validate`.

The validator implementation lives in your app, not in `x/validate`. The
canonical example adapts `go-playground/validator`:

```go
// reference/with-rest/internal/validation/playground/adapter.go
var _ validate.Validator = (*Validator)(nil)
```

### 3. API documentation — `x/openapi`

`x/openapi` generates the spec from the routes you already registered — it reads
`[]router.RouteInfo`, so it works for `x/rest` resources and hand-written routes
alike. It is driven out-of-band by the CLI, not wired into the running app:

```go
// what `plumego generate spec` does internally (cmd/plumego/commands/spec.go):
doc := openapi.New().Generate(app.Core.Routes(), ops)
_ = openapi.WriteYAML(out, doc) // or WriteJSON
```

```bash
plumego generate spec --format yaml --output openapi.yaml
```

`ops` is an optional `map[string]openapi.Op` of per-operation hints (summaries,
request/response schemas) loaded from a spec file; routes without hints still
appear with their method and path. Because generation reads the route table,
the doc stays in sync with what is actually served without reflection or
annotations.

## Putting it together

```
request ─▶ x/rest route ─▶ handler
                              │
                              ├─ validate.Bind[T](r, v)   ── decode + validate body
                              ├─ repository call           ── persistence (your code)
                              └─ contract.WriteResponse    ── success envelope

build time ─▶ plumego generate spec ─▶ openapi.Generate(app.Routes(), hints) ─▶ openapi.yaml
```

The canonical wiring is `reference/with-rest`:

- `internal/app/app.go` — `rest.NewDBResource` wiring
- `internal/validation/playground/adapter.go` — a `validate.Validator` adapter
- `Makefile` — the `plumego generate spec` invocation

## Common mistakes

| Mistake | Correct approach |
|---|---|
| Expecting `x/rest` to validate request bodies | Call `x/validate.Bind` inside the write handler |
| Looking for a built-in validator in `x/validate` | Provide your own `Validator` (e.g. a `go-playground/validator` adapter) |
| Wiring `x/openapi` generation into request handling | Generate the spec at build time via `plumego generate spec` |
| Adding OpenAPI annotations to handlers | Supply operation hints to `Generator.Generate`; routes are read from the route table |
| Importing one of these extensions from another | They stay decoupled; compose them in the application layer |

## Maturity note

All three are **experimental** (`docs/concepts/extension-maturity.md`). APIs may
change; pin behavior with tests at the composition boundary in your app.
