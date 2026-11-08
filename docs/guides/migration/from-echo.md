# Migrating From Echo

This guide maps Echo applications to Plumego's explicit `net/http` model. Echo
centers most request work on `echo.Context`; Plumego keeps request state in
`*http.Request`, response writes on `http.ResponseWriter`, and dependencies in
constructors.

Use the standard-service project shape while migrating: `main.run` creates the
process signal context and calls `app.Start(ctx)`, routes stay in
`internal/app/routes.go`, handlers stay in `internal/handler`, and business
models/repositories live under `internal/domain/<name>`.

## Concept Mapping

| Echo | Plumego |
| --- | --- |
| `echo.Echo` | `core.App` |
| `echo.Group` | `router.Group` via `router.Router.Group` |
| `echo.Context` | `http.ResponseWriter` plus `*http.Request` |
| `echo.HandlerFunc` | `func(http.ResponseWriter, *http.Request)` or `http.HandlerFunc` |
| `echo.HTTPError` | `contract.APIError` built with `contract.NewErrorBuilder` |
| `c.JSON(status, value)` | `contract.WriteResponse(w, r, status, value, nil)` |
| `c.Param("id")` | `contract.RequestParamFromContext(r.Context(), "id")` |
| `c.Bind(&dst)` | `json.NewDecoder(r.Body).Decode(&dst)` or `x/validate.Bind[T]` |
| `echo.MiddlewareFunc` | `func(http.Handler) http.Handler` |

## Handler And JSON Response

Echo handlers return `error`; Plumego handlers write the response directly and
return from the function after any error write.

```go
package users

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumego "github.com/birdordev/plumego/x/validate"
)

type CreateUserRequest struct {
	Name string `json:"name"`
}

type UserResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func CreateUser(w http.ResponseWriter, r *http.Request) {
	req, err := plumego.BindJSON[CreateUserRequest](r)
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeBadRequest).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build())
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusCreated, UserResponse{
		ID:   "u_123",
		Name: req.Name,
	}, nil)
}
```

## Middleware Conversion

Echo middleware wraps `echo.HandlerFunc`. Plumego middleware wraps
`http.Handler` and calls `next.ServeHTTP(w, r)`.

```go
package middleware

import "net/http"

func RequireToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

Key difference: Echo's `c.Next()` pattern does not exist. The middleware itself
chooses whether and when to call `next.ServeHTTP(w, r)`.

## Error Handling Conversion

Translate `echo.NewHTTPError(status, message)` to a `contract.APIError` and
write it through `contract.WriteError`.

```go
package users

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
)

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

	_ = contract.WriteResponse(w, r, http.StatusOK, map[string]string{
		"id": id,
	}, nil)
}
```

## Incompatibilities

- No template rendering surface: use the standard library, an app-local
  renderer, or a dedicated frontend route if the service needs HTML.
- No binder registry: decode request sources explicitly in handlers, or use
  `x/validate.Bind[T]` at the call site.
- No validator hook: wire validators through constructors and call them
  explicitly.
- No `SkipperMiddleware`: branch inside the middleware before calling
  `next.ServeHTTP(w, r)`.

## Next Reads

- `docs/reference/canonical-style-guide.md`
- `reference/standard-service`
