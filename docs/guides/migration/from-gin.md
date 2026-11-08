# Migrating From Gin

This guide maps common Gin patterns to Plumego's standard-library HTTP model.
It is a translation guide, not a compatibility layer. Start from
`reference/standard-service` and keep handlers in the canonical
`func(http.ResponseWriter, *http.Request)` shape.

Use the standard-service project shape while migrating: `main.run` creates the
process signal context and calls `app.Start(ctx)`, routes stay in
`internal/app/routes.go`, handlers stay in `internal/handler`, and business
models/repositories live under `internal/domain/<name>`.

## Concept Mapping

| Gin | Plumego |
| --- | --- |
| `gin.Engine` | `core.App` |
| `gin.RouterGroup` | `router.Group` via `router.Router.Group` |
| `gin.Context` | `*http.Request` plus `http.ResponseWriter` |
| `gin.HandlerFunc` | `func(http.ResponseWriter, *http.Request)` or `http.HandlerFunc` |
| `gin.H` | typed response structs or maps written with `contract.WriteResponse` |
| `gin.AbortWithError` | `contract.WriteError` followed by `return` |
| `c.Param("id")` | `contract.RequestParamFromContext(r.Context(), "id")` |
| `c.ShouldBindJSON(&dst)` | `json.NewDecoder(r.Body).Decode(&dst)` |

## Handler Conversion

Gin handlers usually combine params, binding, validation, and response writing
through `*gin.Context`. In Plumego, keep those steps explicit.

```go
package users

import (
	"encoding/json"
	"net/http"

	"github.com/birdordev/plumego/contract"
)

type CreateUserRequest struct {
	Name string `json:"name"`
}

type UserResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func CreateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build())
		return
	}
	if req.Name == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Detail("field", "name").
			Message("name is required").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusCreated, UserResponse{
		ID:   "u_123",
		Name: req.Name,
	}, nil)
}
```

## Route Group Conversion

Gin route groups hide the prefix behind the group object. Plumego keeps the same
idea on `router.Router`, but registrations are still one method, one path, one
handler.

```go
package routes

import (
	"net/http"

	"github.com/birdordev/plumego/router"
)

func RegisterUserRoutes(r *router.Router) error {
	api := r.Group("/api")
	users := api.Group("/users")

	if err := users.AddRoute(http.MethodGet, "/:id", http.HandlerFunc(ShowUser)); err != nil {
		return err
	}
	if err := users.AddRoute(http.MethodPost, "/", http.HandlerFunc(CreateUser)); err != nil {
		return err
	}
	return nil
}

func ShowUser(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("show user"))
}

func CreateUser(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("create user"))
}
```

## JSON Response Conversion

Replace `c.JSON(status, gin.H{...})` with `contract.WriteResponse`. Prefer typed
response structs when the response shape is part of the API contract.

```go
package status

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
)

type StatusResponse struct {
	Status string `json:"status"`
}

func Status(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteResponse(w, r, http.StatusOK, StatusResponse{
		Status: "ok",
	}, nil)
}
```

## Middleware Translation

Gin middleware returns `gin.HandlerFunc` and usually calls `c.Next()`. Plumego
middleware is the standard `func(http.Handler) http.Handler` shape.

```go
package middleware

import "net/http"

func RequireHeader(name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(name) == "" {
				http.Error(w, "missing header", http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

Register global middleware explicitly near app construction:

```go
if err := app.Use(RequireHeader("X-Request-ID")); err != nil {
	return err
}
```

## Incompatibilities

- No `c.Next()`: middleware calls `next.ServeHTTP(w, r)` exactly where the next
  handler should run.
- No mutable context value bag: pass dependencies through constructors, and use
  request context only for transport metadata.
- No automatic JSON 404/405 response policy: configure routing behavior and
  error responses explicitly for your app.
- No panic recovery by default: add `middleware/recovery` explicitly when the
  service should recover panics.

## Next Reads

- `docs/reference/canonical-style-guide.md`
- `reference/standard-service`
