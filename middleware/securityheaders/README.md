# securityheaders — Security Header Middleware

Injects a strict set of HTTP security headers into every response.

## Import

```go
import "github.com/birdordev/plumego/middleware/securityheaders"
```

## Overview

`securityheaders.Middleware` adds headers such as `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, and `Content-Security-Policy` without requiring per-handler changes.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(config Config)` | Constructor |
| `DefaultConfig()` | Production-oriented defaults |
| `Config{...}` | Optional per-header overrides |

## Usage

```go
app.Use(securityheaders.Middleware(securityheaders.DefaultConfig()))
```

## Notes

- Defaults follow OWASP secure configuration recommendations.
- Customize `Content-Security-Policy` for applications that serve inline scripts or styles.
- This middleware applies to **all** routes; consider skipping it for health/check endpoints if external probes require permissive headers.
