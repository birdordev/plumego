# compression — Gzip Response Compression Middleware

Conditionally compresses HTTP responses with gzip.

## Import

```go
import "github.com/spcent/plumego/middleware/compression"
```

## Overview

`compression.Middleware` uses the negotiated encoding and response content type to choose compression. Gzip output streams once it starts.

## Exports

| Symbol | Description |
|--------|-------------|
| `Middleware(cfg Config)` | Constructor |
| `DefaultConfig()` | Default pre-stream buffer limit: 10 MiB |
| `Config{MaxBufferBytes}` | Buffer limit before gzip output starts |

## Usage

```go
app.Use(compression.Middleware(compression.DefaultConfig()))
```

## Notes

- The middleware ignores already-compressed content types (`image/*`, `video/*`, `application/gzip`, etc.).
- `Content-Encoding: gzip` is set only when compression is actually applied.
- There is no minimum-size threshold. A response that exceeds `MaxBufferBytes`
  before gzip starts is passed through; once gzip starts, later writes stay in
  the same compressed stream.
- Informational 100/103 responses are forwarded without committing the final
  status or choosing compression. A later final success or error keeps its
  status and appropriate encoding. A 101 protocol switch is a final transition.
  Each informational response uses a temporary header snapshot; headers deleted
  afterward do not leak into later informational or final responses. Normal
  header-only completion commits the final headers with an implicit 200, even
  after an informational response. Empty responses are not gzip-encoded.
- The wrapped `ResponseWriter` supports `Unwrap()` for access to the underlying writer.
