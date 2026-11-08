# cors — Cross-Origin Resource Sharing Middleware

Controls browser cross-origin request policies.

## Import

```go
import "github.com/birdordev/plumego/middleware/cors"
```

## Overview

`cors.Middleware` adds CORS headers to responses based on configurable origin, method, header, and credential policies.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(opts CORSOptions)` | Constructor |
| `CORSOptions` | Allowed origins, methods, headers, credentials, etc. |
| `StrictDefaultOptions()` | Production defaults requiring explicit origins, rejecting wildcards |
| `ErrStrictDefaultOriginsRequired` | Returned when strict defaults have no explicit origin |
| `ErrStrictDefaultWildcardOrigin` | Returned when strict defaults use wildcard origin |

## Usage

```go
// Public/open API (allows any origin)
app.Use(cors.Middleware(cors.CORSOptions{}))

// Production (explicit origins only)
opts, err := cors.StrictDefaultOptions("https://app.example.com")
if err != nil {
    return err
}
app.Use(cors.Middleware(opts))
```

## Security Notes

- When `AllowedOrigins` is empty, the default is `["*"]`. This is an **open CORS policy**.
- Do not allow wildcards when `AllowCredentials` is true.
- Prefer `StrictDefaultOptions` for production services.
