# x/openapi

`x/openapi` generates OpenAPI 3.1 document structures from explicit
`router.RouteInfo` values and optional operation hints. It does not inspect
handler functions and does not depend on an external OpenAPI library.

Status: **experimental**. The API may change between minor releases.

---

## Quick Start

```bash
# Generate an OpenAPI spec from a project following the standard-service layout
cd myproject
plumego generate spec --format json --output openapi.json

# YAML output
plumego generate spec --format yaml --output openapi.yaml

# Stdout
plumego generate spec
```

The command locates `./internal/app` as the application package, calls
`app.RegisterRoutes()` to collect `router.RouteInfo` values, and then runs
`openapi.New().Generate(routes, hints)` to produce the document. The temporary
generator uses the target project's selected Plumego version and module
replacements.

---

## Operation Hints (`plumego.spec.yaml`)

By default, every supported route method gets a minimal operation with
`"200": {description: "OK"}`. Route placeholders produce required string
path parameters. A matching path parameter hint supplies its schema and
description, and the generated parameter remains required. Query hints
are independent of path parameters with the same name.

Place a `plumego.spec.yaml` file in the project root to annotate routes:

```yaml
# plumego.spec.yaml
info:
  title: My API
  version: 1.0.0

operations:
  # Key is either the route's registered name or "METHOD /path"
  getUserByID:
    summary: Get a user by ID
    tags: [users]
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: string
    responses:
      "200":
        description: User found
      "404":
        description: User not found

  "POST /users":
    summary: Create a user
    requestBody:
      required: true
      content:
        application/json:
          schema:
            type: object
```

The hints file is optional. Routes without a hint entry receive default
`"200"` responses.

---

## Go API

Use the Go API directly when generating documents programmatically:

```go
import (
    "github.com/birdordev/plumego/x/openapi"
    "github.com/birdordev/plumego/router"
)

// Collect route metadata (e.g. from app.Core.Routes() after RegisterRoutes).
routes := []router.RouteInfo{...}

// Build optional operation hints.
hints := map[string]openapi.Op{
    "listUsers": {
        Summary: "List all users",
        Tags:    []string{"users"},
        Responses: map[string]openapi.Response{
            "200": {Description: "OK"},
        },
    },
    "POST /users": {
        Summary: "Create a user",
        Body: &openapi.RequestBody{
            Required: true,
            Content:  openapi.JSONContent(openapi.Schema{Type: "object"}),
        },
        Responses: map[string]openapi.Response{
            "201": {Description: "Created"},
        },
    },
}

// Generate the document.
gen := openapi.New()
doc := gen.Generate(routes, hints)

// Serialise.
if err := openapi.WriteJSON(os.Stdout, doc); err != nil {
    log.Fatal(err)
}
```

---

## Document Model

| Type | Purpose |
|---|---|
| `Document` | Root OpenAPI 3.1 document (`openapi`, `info`, `paths`) |
| `Info` | Document metadata (`title`, `version`) |
| `PathItem` | Per-path operations (Get, Post, Put, Patch, Delete, Head, Options, Trace) |
| `Operation` | Single HTTP method operation with summary, tags, parameters, requestBody, responses |
| `Op` | Operation hint supplied by callers; merged into the generated `Operation` |
| `Param` | Path, query, or header parameter |
| `Schema` | Minimal OpenAPI schema (type, format, properties, items, $ref) |
| `RequestBody` | Request body descriptor with content map |
| `Response` | Response descriptor with description and optional content |
| `MediaType` | Media type descriptor with schema |

The experimental `PathItem.Trace` field holds an explicitly registered
`TRACE` operation. JSON and YAML omit the field on paths without that method.

---

## Schema Helpers

```go
// Pre-defined primitive schemas.
openapi.String   // {type: "string"}
openapi.Integer  // {type: "integer"}
openapi.Boolean  // {type: "boolean"}
openapi.Array    // {type: "array"}

// Parameter constructors.
openapi.PathParam("id", openapi.String)             // required path param
openapi.QueryParam("page", openapi.Integer)         // optional query param
openapi.HeaderParam("X-Request-ID", openapi.String) // optional header param

// JSON content helper for requestBody and response hints.
openapi.JSONContent(openapi.Schema{Type: "object"})
// → map[string]MediaType{"application/json": {Schema: {Type: "object"}}}
```

---

## Serialisation

Both JSON and YAML are supported without external dependencies:

```go
// JSON (pretty-printed via encoding/json MarshalIndent)
data, err := openapi.MarshalJSON(doc)
err = openapi.WriteJSON(w, doc)

// YAML (hand-written emitter built on encoding/json; stdlib only)
data, err := openapi.MarshalYAML(doc)
err = openapi.WriteYAML(w, doc)
```

---

## Path Template Conversion

Route paths using `:param` or `*wildcard` notation are converted to OpenAPI
`{param}` templates automatically during generation:

| Router path | OpenAPI path |
|---|---|
| `/users/:id` | `/users/{id}` |
| `/files/*path` | `/files/{path}` |
| `/api/v1/users/:id/posts/:postId` | `/api/v1/users/{id}/posts/{postId}` |

---

## Hint Lookup Order

When merging hints, the generator tries:

1. The route's `Meta.Name` (if the route was registered with a name)
2. `"METHOD /path"` (e.g. `"GET /users/:id"`)
3. Default — minimal operation with `"200"` response

---

## Boundary Rules

- `x/openapi` must not be imported by stable roots.
- `x/openapi` does not inspect handler functions via reflection.
- `x/openapi` does not depend on an external OpenAPI validation or YAML library.
- Serialization uses `encoding/json` and a hand-written YAML emitter (stdlib only).
- The CLI integration lives in `cmd/plumego/commands/spec.go`, not in `x/openapi`.

---

## Validation

```bash
go test -race -timeout 60s ./x/openapi/...
go vet ./x/openapi/...
```

---

## Related

- `cmd/plumego/commands/generate.go` — `plumego generate spec` CLI subcommand
- `reference/standard-service` — canonical app layout with `app.Core.Routes()` wiring
- `router.RouteInfo` — the metadata type driving document generation

## Strict generation

`plumego generate spec --strict` calls `GenerateStrict` and requires a response
hint for every supported operation before writing the output. Strict mode
rejects equivalent path templates with different parameter names, duplicate
parameters within the same location, invalid parameter locations, unmatched
path parameter hints, and content on responses that cannot carry a
body, including HEAD and HTTP 204. The CLI can
load `components.securitySchemes` and per-operation `security` from the hints
file. Strict generation needs a target application using a library checkout or
release that provides `GenerateStrict`. The standard-service
`plumego.spec.yaml` describes the protected write profile.

`GenerateStrict` reports unused route hints, missing or invalid authentication
schemes, duplicate operation IDs, and unresolved local schema references.
Strict mode supports HTTP authentication with a scheme token, API keys with a
name and a `header`, `query`, or `cookie` location, and mutual TLS. OAuth 2.0 and
OpenID Connect definitions require fields outside the current scheme model.
Fragment pointers can refer to nested properties and use percent-encoding and JSON
Pointer escapes. External references are preserved without resolution. Configure
`components.schemas` and `components.securitySchemes` via
`WithComponents` in Go or the hint file in the CLI. Operation hints can
supply `operationId`. A named route provides the default operation ID.
