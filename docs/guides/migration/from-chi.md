# Migrating From Chi

Chi and Plumego are both standard-library HTTP stacks. Most handlers,
middleware, and tests can move with small edits. The main changes are route
registration, route metadata, application lifecycle wiring, and response/error
helpers.

Use the standard-service project shape while migrating: `main.run` creates the
process signal context and calls `app.Start(ctx)`, routes stay in
`internal/app/routes.go`, handlers stay in `internal/handler`, and business
models/repositories live under `internal/domain/<name>`.

## Compatibility Statement

Chi-compatible middleware already has the Plumego middleware shape:
`func(http.Handler) http.Handler`. You can keep those middleware functions and
register them with `app.Use(...)` or compose them with `middleware.NewChain`.

## Concept Mapping

| Chi | Plumego |
| --- | --- |
| `chi.NewRouter()` | `router.NewRouter()` for a bare router, or `core.New(...)` for an application |
| `chi.Route(...)` | `router.Router.Group(...)` plus `AddRoute(...)` |
| `r.With(...)` | `middleware.NewChain(...).Build(handler)` |
| `chi.URLParam(r, "id")` | `contract.RequestParamFromContext(r.Context(), "id")` |
| `chi.RouteContext(r.Context())` | `contract.RequestContextFromContext(r.Context())` |
| `r.Use(...)` | `app.Use(...)` for global application middleware |

## Handler Delta

Keep normal `net/http` handlers. Replace ad-hoc response writing with
`contract.WriteResponse` or `contract.WriteError` when the endpoint is part of a
JSON API.

```go
package users

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
)

type UserResponse struct {
	ID string `json:"id"`
}

func ShowUser(w http.ResponseWriter, r *http.Request) {
	id := contract.RequestParamFromContext(r.Context(), "id")
	if id == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Detail("field", "id").
			Message("id is required").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, UserResponse{ID: id}, nil)
}
```

## Route Names And Reverse URLs

Chi route names commonly live near route patterns. Plumego uses
`router.WithRouteName` as route metadata and resolves URLs through
`router.Router.URL`.

```go
package routes

import (
	"net/http"

	"github.com/birdordev/plumego/router"
)

func Register(r *router.Router) error {
	if err := r.AddRoute(
		http.MethodGet,
		"/users/:id",
		http.HandlerFunc(ShowUser),
		router.WithRouteName("users.show"),
	); err != nil {
		return err
	}
	return nil
}

func UserURL(r *router.Router, id string) string {
	return r.URL("users.show", "id", id)
}

func ShowUser(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("show user"))
}
```

## Group Prefix Stacking

Chi's `r.Route("/api", func(r chi.Router) { ... })` and Plumego's
`router.Group("/api")` express the same idea with different syntax.

```go
package routes

import (
	"net/http"

	"github.com/birdordev/plumego/router"
)

func RegisterAPI(r *router.Router) error {
	api := r.Group("/api")
	v1 := api.Group("/v1")

	if err := v1.AddRoute(http.MethodGet, "/users/:id", http.HandlerFunc(ShowUser)); err != nil {
		return err
	}
	return nil
}

func ShowUser(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("show user"))
}
```

## Middleware And Tests To Keep

Keep existing chi-compatible middleware and `httptest` tests. Both are already
standard `net/http`.

```go
package chain

import (
	"net/http"

	plumemw "github.com/birdordev/plumego/middleware"
)

func AddHeader(name, value string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(name, value)
			next.ServeHTTP(w, r)
		})
	}
}

func WrapEndpoint(endpoint http.Handler) http.Handler {
	return plumemw.NewChain(
		AddHeader("X-Service", "users"),
	).Build(endpoint)
}
```

## What To Change

- Swap `chi.NewRouter()` as the application root for `core.New(...)` and
  explicit route registration through `core.App`.
- Create the process signal context in `main.run` and pass it to
  `app.Start(ctx)` instead of hiding lifecycle ownership inside app wiring.
- Use `router.NewRouter()` only when you need a bare router outside a full app.
- Use `contract.WriteResponse` and `contract.WriteError` instead of ad-hoc JSON
  helpers.
- Use `contract.RequestParamFromContext` for route params when you want the
  transport contract surface; `router.Param` remains available for router-local
  code.

## What To Keep

- Existing `func(http.ResponseWriter, *http.Request)` handlers.
- Existing `func(http.Handler) http.Handler` middleware.
- Existing `httptest.NewRecorder` and `httptest.NewRequest` tests.
- Request-scoped cancellation and deadlines through `r.Context()`.

## Next Reads

- `reference/standard-service`
- `docs/reference/canonical-style-guide.md`
