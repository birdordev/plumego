# Middleware Compatibility

Plumego middleware uses the standard shape:

```go
func(http.Handler) http.Handler
```

Any package that already exposes that shape can be used directly with
`app.Use(...)` or `middleware.NewChain(...)`. This guide shows how to adapt a
few common middleware styles without adding compatibility shims to Plumego
itself.

## net/http Middleware

Middleware already written as `func(http.Handler) http.Handler` is directly
compatible.

```go
package compat

import "net/http"

func AddHeader(name, value string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(name, value)
			next.ServeHTTP(w, r)
		})
	}
}
```

Use it unchanged:

```go
if err := app.Use(AddHeader("X-Service", "users")); err != nil {
	return err
}
```

## gorilla/handlers Style

Many gorilla-style helpers return an `http.Handler` after receiving the next
handler. Wrap that call in a middleware literal.

```go
package compat

import "net/http"

func WrapHandlerFactory(factory func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return factory(next)
	}
}

func ExampleFactory(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Wrapped", "true")
		next.ServeHTTP(w, r)
	})
}
```

Register the adapter:

```go
if err := app.Use(WrapHandlerFactory(ExampleFactory)); err != nil {
	return err
}
```

## negroni Style

Negroni-style middleware receives a special `next` handler in `ServeHTTP`.
Convert it by constructing the next-aware handler inside the Plumego wrapper.

```go
package compat

import "net/http"

type NegroniHandler interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc)
}

func FromNegroni(h NegroniHandler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r, next.ServeHTTP)
		})
	}
}

type ExampleNegroni struct{}

func (ExampleNegroni) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	w.Header().Set("X-Negroni", "true")
	next(w, r)
}
```

Register the adapter:

```go
if err := app.Use(FromNegroni(ExampleNegroni{})); err != nil {
	return err
}
```

## alice Style

Alice constructors already use the same middleware type. Use them as-is in
`middleware.NewChain`.

```go
package compat

import (
	"net/http"

	plumemw "github.com/birdordev/plumego/middleware"
)

type Constructor func(http.Handler) http.Handler

func AddTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace", "demo")
		next.ServeHTTP(w, r)
	})
}

func Chain(endpoint http.Handler) http.Handler {
	return plumemw.NewChain(
		Constructor(AddTrace),
	).Build(endpoint)
}
```

## Multiple Next Calls

Plumego middleware should call `next.ServeHTTP(w, r)` at most once. Middleware
that retries or fan-outs by calling the next handler multiple times can produce
duplicate writes, broken response headers, and confusing logs.

Detect the issue by reading the middleware body before adapting it:

- exactly zero calls means the middleware blocks the request path
- exactly one call is the normal wrapper shape
- more than one call should be rewritten so retries happen in a service/client
  dependency, not by invoking the downstream HTTP handler again

When adapting third-party middleware, keep the wrapper boring: run pre-work,
call `next.ServeHTTP(w, r)` once, then run post-work only if the middleware owns
that post-response behavior.

## Next Reads

- `docs/guides/migration/from-gin.md`
- `docs/guides/migration/from-echo.md`
- `docs/guides/migration/from-chi.md`
- `docs/reference/canonical-style-guide.md`
