# x/validate

`x/validate` provides explicit request binding helpers for handlers that want
JSON decoding plus caller-owned validation without introducing validation
middleware, a tag language, or a third-party dependency.

Status: **experimental**. The API may change between minor releases.

---

## When to Use

- The handler needs to decode a JSON request body and validate the result.
- Validation errors should be returned as structured `contract.APIError` responses.
- The validation rules are caller-defined (no tag-based or annotation-based approach).

## Do Not Use For

- Middleware-level request validation before the handler runs.
- Tag-based struct validation (`validate:"required"` etc.) — keep that in your
  own adapter or an external module.
- Stable-root binding behavior (does not belong in `contract`).

---

## Core API

```go
import "github.com/birdordev/plumego/x/validate"

// BindJSON decodes the request body into T without validation.
// Returns a *validate.ValidationError with contract.APIError shape on decode failure.
func BindJSON[T any](r *http.Request) (T, error)

// Bind decodes into T and then calls v.Validate(&result).
// Returns a *validate.ValidationError if decoding or validation fails.
func Bind[T any](r *http.Request, v validate.Validator) (T, error)

// Validator is the interface your validation logic must satisfy.
type Validator interface {
    Validate(v any) error
}
```

---

## BindJSON — Decode Only

Use `BindJSON` when your handler only needs to decode the body and does its
own validation inline:

```go
import (
    "net/http"
    "github.com/birdordev/plumego/contract"
    "github.com/birdordev/plumego/x/validate"
)

type CreateUserRequest struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
    req, err := validate.BindJSON[CreateUserRequest](r)
    if err != nil {
        _ = contract.WriteError(w, r, err)
        return
    }

    if req.Name == "" || req.Email == "" {
        apiErr := contract.NewErrorBuilder().
            WithType(contract.TypeValidation).
            WithMessage("name and email are required").
            Build()
        _ = contract.WriteError(w, r, apiErr)
        return
    }

    _ = contract.WriteResponse(w, r, http.StatusCreated, map[string]string{"id": "new-id"}, nil)
}
```

---

## Bind — Decode + Validate

Use `Bind` when you have a `Validator` implementation. The validator runs after
a successful decode; its errors are adapted to `contract.APIError`:

```go
// Define your validator (in your own package, not in x/validate).
type userRequestValidator struct{}

func (userRequestValidator) Validate(v any) error {
    req, ok := v.(*CreateUserRequest)
    if !ok {
        return fmt.Errorf("unexpected type %T", v)
    }
    if req.Name == "" {
        return fmt.Errorf("name is required")
    }
    if req.Email == "" {
        return fmt.Errorf("email is required")
    }
    return nil
}

// Use Bind in your handler.
func createUserHandler(w http.ResponseWriter, r *http.Request) {
    req, err := validate.Bind[CreateUserRequest](r, userRequestValidator{})
    if err != nil {
        _ = contract.WriteError(w, r, err)
        return
    }
    _ = contract.WriteResponse(w, r, http.StatusCreated, req, nil)
}
```

---

## Third-Party Validator Adapter

Keep the adapter in your application code or an external module — not in
`x/validate`. Example with `github.com/go-playground/validator/v10`:

```go
// internal/validation/adapter.go  (your application code)
package validation

import (
    "github.com/go-playground/validator/v10"
    xvalidate "github.com/birdordev/plumego/x/validate"
)

type PlaygroundAdapter struct{ v *validator.Validate }

func New() xvalidate.Validator {
    return &PlaygroundAdapter{v: validator.New()}
}

func (a *PlaygroundAdapter) Validate(val any) error {
    return a.v.Struct(val)
}
```

```go
// handler.go
type CreateUserRequest struct {
    Name  string `json:"name"  validate:"required"`
    Email string `json:"email" validate:"required,email"`
}

var v = validation.New()

func createUserHandler(w http.ResponseWriter, r *http.Request) {
    req, err := validate.Bind[CreateUserRequest](r, v)
    if err != nil {
        _ = contract.WriteError(w, r, err)
        return
    }
    // req is validated and decoded
}
```

See `reference/with-rest` for a complete working example.

---

## Error Shape

Both `BindJSON` and `Bind` return `*validate.ValidationError` on failure.
`contract.WriteError` handles this type natively:

```
HTTP 422 Unprocessable Entity
{
  "error": {
    "type": "validation_error",
    "message": "request body invalid: <detail>",
    "code": "VALIDATION_ERROR"
  }
}
```

Decode errors (malformed JSON, EOF) produce `TypeBadRequest`.
Validation errors from `Validator.Validate` produce `TypeValidation`.

---

## Boundary Rules

- `x/validate` imports only `contract` and the standard library.
- Validation tag languages, reflection-based parsers, and third-party
  validators belong in application code or external modules, not in `x/validate`.
- Validation runs at explicit handler call sites, not as middleware.
- No default global validator instance exists or should be added.

---

## Validation

```bash
go test -race -timeout 60s ./x/validate/...
go vet ./x/validate/...
```
