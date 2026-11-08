# auth — Authentication & Authorization Middleware

Provides `Authenticate` and `Authorize` adapters for request-level identity and access control.

## Import

```go
import "github.com/birdordev/plumego/middleware/auth"
```

## Overview

`auth` wraps Plumego's `authn` primitives as `http.Handler` middleware. It handles bearer-token extraction, realm negotiation, and 401/403 response generation without leaking internal policy details.

## Exports

| Symbol | Description |
|--------|-------------|
| `Authenticate(authenticator, opts...)` | Returns middleware that injects identity into context or returns 401 |
| `Authorize(authorizer, action, resource, opts...)` | Returns middleware that checks permission or returns 403 |
| `AuthorizeFunc(authorizer, resolver, opts...)` | Like `Authorize` but resolves action/resource from the request |
| `AuthErrorHandler` | Callback for customizing 401/403 responses |
| `WithAuthErrorHandler`, `WithAuthRealm` | Constructor options |

## Usage

```go
mw, err := auth.Authenticate(myAuthenticator)
if err != nil {
    return err
}
app.Use(mw)
```

Authorization after authentication:

```go
app.Use(
    auth.Authorize(myAuthorizer, "read", "orders"),
)
```

## Notes

- `Authenticate` should appear before `Authorize` in the middleware chain.
- The default error handler returns `WWW-Authenticate: Bearer realm="..."` for 401s.
- Realm values are sanitized to prevent response-header injection.
