# Frontend Package

The `frontend` package provides static file serving for built frontend applications (React, Vue, Next.js, Vite, etc.) with advanced features for production deployments.

For simple stable file mounts without frontend asset policy, use `router.Static` or `router.StaticFS`. Keep cache headers, SPA fallback, pre-compressed assets, custom headers, custom error pages, and MIME overrides in this package.

Directory-backed mounts created with `RegisterFromDir`, `NewMountFromDir`, or
`http.Dir` inputs passed to `RegisterFS`, `NewMountFS`, and `NewHandlerFS`
resolve the configured directory to an absolute canonical path during
construction. They also fail fast if the configured index file is missing or is a
directory. Other caller-provided `http.FileSystem` values remain lazy because
they may be embedded, generated, or remote-backed.

Directory-backed bundles are treated as static deployment artifacts. The
precompressed variant plan is construction-time metadata, not a runtime atomic
snapshot. If a deployment updates files while the process is serving requests,
replace the bundle atomically outside this package, for example by switching a
release directory or restarting with a new immutable asset root.

For directory-backed mounts with precompression enabled, available `.br` and
`.gz` variants are indexed once during construction. This keeps per-request
variant decisions deterministic and avoids probing the filesystem for every
uncompressed response. Accepted planned variants are tried before opening the
original file, while still verifying the current original path exists inside the
mounted directory. Scan errors fail mount construction. Missing or unreadable
compressed variants are a best-effort downgrade: if identity is acceptable, the
original asset is served instead, and no log or metric is emitted by this
package unless `WithPrecompressedVariantMissHandler` is configured. Add that
application-owned hook if stale or missing build artifacts need an operational
signal. Non-`http.Dir` custom filesystems keep lazy variant probing. That means
original responses can probe `.br` and `.gz` candidates to decide whether
`Vary: Accept-Encoding` is required; use directory-backed mounts when those
extra backend opens are too expensive, or provide
`WithPrecompressedVariantPlan` when a custom filesystem already has reliable
variant metadata.

Mount registration uses a fixed ANY-route plan: root mounts register `/` and
`/*filepath`; prefixed mounts register `<prefix>/*filepath` and `<prefix>`.
When the registrar exposes route snapshots, duplicate target routes are rejected
before any frontend route is added. AddRoute-only custom registrars keep
best-effort sequential registration and may be left partially registered if a
later route add fails; use `router.Router` or another snapshot-capable
registrar when atomic duplicate preflight is required.

`Mount.Prefix` returns an empty string for a nil mount, and `Mount.Handler`
returns nil for a nil mount. Those nil receiver results are part of the public
inspection contract; `Mount.Register` still rejects nil mounts and nil
registrars.

## Features

- ✅ **Dual Source Support**: Serve from disk directories or embedded filesystems
- ✅ **SPA Routing**: Automatic fallback to `index.html` for client-side routing
- ✅ **Pre-compressed Files**: Serve `.gz` and `.br` files automatically when accepted by the client
- ✅ **Custom Error Pages**: Configure custom 404 and 5xx error pages
- ✅ **MIME Type Overrides**: Set custom content types for specific file extensions
- ✅ **Cache Control**: Separate caching strategies for index vs. assets
- ✅ **Security**: Path traversal, directory symlink escape protection, and method restrictions
- ✅ **Custom Headers**: Apply security headers or custom metadata
- ✅ **Flexible Mounting**: Mount at any URL prefix (`/`, `/app`, etc.)

## Quick Start

### Basic Usage

```go
import (
    "github.com/birdordev/plumego/router"
    "github.com/birdordev/plumego/x/frontend"
)

r := router.NewRouter()

// Construct a mount first, then decide when to register it
mount, err := frontend.NewMountFromDir("./dist",
    frontend.WithCacheControl("public, max-age=31536000"),
    frontend.WithIndexCacheControl("no-cache"),
)
if err != nil {
    panic(err)
}
err = mount.Register(r)
```

### Embedded Filesystem

```go
import (
    "embed"
    "io/fs"
    "net/http"
)

//go:embed dist/*
var distFS embed.FS

func setupFrontend(r *router.Router) {
    subFS, _ := fs.Sub(distFS, "dist")
    mount, _ := frontend.NewMountFS(http.FS(subFS),
        frontend.WithPrefix("/"),
        frontend.WithFallback(true),
    )
    _ = mount.Register(r)
}
```

## Configuration Options

The option surface is intentionally sealed. `Option` is exported so constructors
can accept `With*` values consistently, but its target config type is
package-private. Applications should compose the exported `With*` options
instead of defining custom options against internal state. New configuration
knobs should be added as explicit `With*` helpers so the stable API stays
reviewable and snapshot-friendly. Options that accept maps copy those maps when
the exported helper is called, so later caller mutations do not affect mount
construction or response behavior.

## Stable Candidate API

`x/frontend` is still experimental, but the public surface that must be frozen
before promotion is:

- Registration and construction: `RegisterFromDir`, `RegisterFS`,
  `NewMountFromDir`, `NewMountFS`, and `NewHandlerFS`.
- Mount contract: `Mount`, `Mount.Prefix`, `Mount.Handler`, and
  `Mount.Register`.
- Router boundary: `Registrar`.
- Configuration: sealed `Option` values produced by `WithPrefix`, `WithIndex`,
  `WithCacheControl`, `WithIndexCacheControl`, `WithFallback`, `WithHeaders`,
  `WithPrecompressed`, `WithNotFoundPage`, `WithErrorPage`, and
  `WithMIMETypes`, plus `WithPrecompressedVariantMissHandler` and
  `PrecompressedVariantMiss`, `WithPrecompressedVariantPlan`, and
  `PrecompressedVariants`.

Stable-compatible changes should add new exported helpers or constructors and
be reviewed through release-backed API snapshots. They should not require users
to implement custom options against package-private config state.

### WithPrefix(prefix string)

Mount the frontend at a specific URL prefix.

```go
// Mount at /app
frontend.WithPrefix("/app")

// Access via: /app, /app/assets/*, /app/page1, etc.
```

**Default**: `/`

### WithIndex(filename string)

Override the default index file name.

```go
frontend.WithIndex("main.html")
```

**Default**: `index.html`

### WithCacheControl(header string)

Set `Cache-Control` header for asset files (JS, CSS, images, etc.).

```go
frontend.WithCacheControl("public, max-age=31536000, immutable")
```

**Default**: No caching (empty)

Unsafe header values containing CR, LF, NUL, or other control characters are
rejected during mount construction.

### WithIndexCacheControl(header string)

Set `Cache-Control` header for index file responses.

```go
frontend.WithIndexCacheControl("no-cache, must-revalidate")
```

**Default**: No caching (empty)

**Why separate?** Index files should not be cached (to ensure users get updates), while assets with hashed names can be cached forever.

Unsafe header values follow the same validation as `WithCacheControl`.

### WithFallback(enabled bool)

Enable SPA fallback mode. When enabled, missing navigation-like requests return
`index.html` instead of 404.

```go
frontend.WithFallback(true)  // Enable for SPAs
frontend.WithFallback(false) // Disable for static sites
```

**Default**: `true`

Fallback is intentionally navigation-only. Missing paths with a file extension
such as `/assets/app.js`, `/app.css`, `/module.wasm`, or `/app.js.map` return
404 instead of the index. Extensionless paths fall back when the request has no
`Accept` header or accepts HTML (`text/html`, `application/xhtml+xml`, `text/*`,
or `*/*`). Requests that explicitly do not accept HTML return 404.

### WithPrecompressed(enabled bool)

Enable automatic serving of pre-compressed files (`.gz`, `.br`).

When enabled:
- Request for `app.js` → serves `app.js.br` if client supports Brotli
- Request for `app.js` → serves `app.js.gz` if client supports Gzip
- Request for `app.js` → serves `app.js` if no pre-compressed version exists
- `Accept-Encoding` quality factors are respected; tokens with `q=0` are not used
- Invalid or out-of-range `q` values make that encoding token invalid
- Requests that refuse `identity` receive `406 Not Acceptable` when no accepted
  pre-compressed variant is available
- Missing or unreadable pre-compressed variants downgrade to the original asset
  when `identity` is acceptable; directory scan errors still fail mount
  construction
- Applications can observe planned variant open misses and candidate stat misses
  with `WithPrecompressedVariantMissHandler`
- Responses for URLs with pre-compressed variants include `Vary: Accept-Encoding`
- Non-`http.Dir` custom filesystems are probed lazily for `.br` and `.gz`
  variants, including on original responses when `Vary: Accept-Encoding` must be
  decided. Directory-backed mounts avoid that per-request variant probing, and
  custom filesystems can avoid it with `WithPrecompressedVariantPlan`.

```go
frontend.WithPrecompressed(true)
```

**Benefits**:
- Faster responses (no runtime compression)
- Lower CPU usage
- Better compression ratios (pre-compressed at build time)

**Build Setup**:
```bash
# Using Brotli (best compression)
find dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' \) \
    -exec brotli -q 11 -o {}.br {} \;

# Using Gzip (widely supported)
find dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' \) \
    -exec gzip -9 -k {} \;
```

**Default**: `false`

### WithPrecompressedVariantMissHandler(handler func(PrecompressedVariantMiss))

Observe accepted pre-compressed candidates that cannot be served and are treated
as best-effort misses.

```go
frontend.WithPrecompressedVariantMissHandler(func(miss frontend.PrecompressedVariantMiss) {
    log.Printf("frontend precompressed miss path=%s variant=%s encoding=%s operation=%s",
        miss.Path, miss.VariantPath, miss.Encoding, miss.Operation)
})
```

The hook is application-owned and runs synchronously while serving the request.
It receives logical asset paths relative to the frontend filesystem root and
does not expose raw filesystem errors. Directory-backed planned variants report
open misses when an indexed variant disappears or becomes unreadable. Any
accepted variant that opens but cannot provide usable metadata reports a `stat`
miss. When the handler is nil, x/frontend emits no log or metric and keeps the
same best-effort downgrade behavior.

### WithPrecompressedVariantPlan(map[string]PrecompressedVariants)

Provide pre-compressed variant metadata for custom filesystems.

```go
frontend.WithPrecompressedVariantPlan(map[string]frontend.PrecompressedVariants{
    "assets/app.js":  {Brotli: true, Gzip: true},
    "assets/app.css": {Gzip: true},
})
```

Use this when a non-directory `http.FileSystem` would make per-request `.br` and
`.gz` miss probes too expensive. Paths are original asset paths relative to the
frontend filesystem root. The option is used only when x/frontend cannot build a
directory-backed plan itself. Callers own the correctness of the metadata; a
planned custom filesystem can serve an accepted variant before opening the
original asset. Invalid or unsafe paths are rejected during mount construction.

### WithNotFoundPage(path string)

Set a custom 404 error page. Path is relative to the filesystem root.

```go
frontend.WithNotFoundPage("404.html")
```

**Example** `404.html`:
```html
<!DOCTYPE html>
<html>
<head><title>Page Not Found</title></head>
<body>
    <h1>404 - Page Not Found</h1>
    <p>The page you're looking for doesn't exist.</p>
    <a href="/">Go Home</a>
</body>
</html>
```

**Default**: Standard `http.NotFound` response

The page path must be a relative asset path. Absolute paths, parent traversal
segments, and backslash-separated paths are rejected during mount construction.
Custom 404 pages receive the configured custom headers and MIME overrides but
do not inherit long-lived asset cache headers.

### WithErrorPage(path string)

Set a custom 5xx error page for server errors.

```go
frontend.WithErrorPage("500.html")
```

**Default**: A `contract.WriteError` JSON response

The page path follows the same validation and cache behavior as
`WithNotFoundPage`.

### WithMIMETypes(types map[string]string)

Override MIME types for specific file extensions.

```go
frontend.WithMIMETypes(map[string]string{
    ".wasm": "application/wasm",
    ".json": "application/json; charset=utf-8",
    ".webp": "image/webp",
})
```

**Use Cases**:
- WebAssembly files (`.wasm`)
- Custom file formats
- Charset specification
- Modern image formats (WebP, AVIF)

**Default**: Uses `http.ServeContent` auto-detection

MIME type values are written as `Content-Type` headers, so values containing CR,
LF, NUL, or other control characters are rejected during mount construction.
Extension keys may be provided with or without the leading dot, but they must be
single file extensions such as `.wasm` or `wasm`. Empty keys, path-like keys,
multi-extension keys such as `.tar.gz`, whitespace, and control characters are
rejected because they cannot be matched predictably with `path.Ext`.

### WithHeaders(headers map[string]string)

Apply custom metadata or security headers to successful file responses.

```go
frontend.WithHeaders(map[string]string{
    "X-Frame-Options":        "DENY",
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy":        "strict-origin-when-cross-origin",
})
```

`WithHeaders` is not a replacement for the package's file-serving policy
options. Transport-critical and internally managed headers are rejected during
mount construction, including hop-by-hop headers plus `Accept-Ranges`,
`Cache-Control`, `Content-Encoding`, `Content-Length`, `Content-Range`,
`Content-Type`, `ETag`, `Last-Modified`, `Transfer-Encoding`, and `Vary`.
Use `WithCacheControl`, `WithIndexCacheControl`, `WithMIMETypes`, and
`WithPrecompressed` for those response semantics.

## Production Configuration

Recommended production setup with all features:

```go
err := frontend.RegisterFromDir(r, "./dist",
    // Mounting
    frontend.WithPrefix("/"),
    frontend.WithIndex("index.html"),

    // Caching Strategy
    frontend.WithCacheControl("public, max-age=31536000, immutable"),
    frontend.WithIndexCacheControl("no-cache, must-revalidate"),

    // Performance
    frontend.WithPrecompressed(true), // Serve .gz/.br files

    // Routing
    frontend.WithFallback(true), // SPA mode

    // Custom Error Pages
    frontend.WithNotFoundPage("404.html"),
    frontend.WithErrorPage("500.html"),

    // MIME Types
    frontend.WithMIMETypes(map[string]string{
        ".wasm": "application/wasm",
        ".json": "application/json; charset=utf-8",
    }),

    // Security Headers
    frontend.WithHeaders(map[string]string{
        "X-Frame-Options":        "DENY",
        "X-Content-Type-Options": "nosniff",
        "Referrer-Policy":        "strict-origin-when-cross-origin",
        "Permissions-Policy":     "geolocation=(), microphone=()",
    }),
)
```

## Build Optimization Guide

### Pre-compression Workflow

1. **Build your frontend**:
   ```bash
   npm run build
   # Output: dist/
   ```

2. **Pre-compress assets**:
   ```bash
   # Brotli (best compression, ~20% better than gzip)
   find dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' -o -name '*.svg' \) \
       -exec brotli -q 11 -o {}.br {} \;

   # Gzip (universal compatibility)
   find dist -type f \( -name '*.js' -o -name '*.css' -o -name '*.html' -o -name '*.svg' \) \
       -exec gzip -9 -k {} \;
   ```

3. **Embed if desired**:
   ```go
   //go:embed dist/*
   var distFS embed.FS
   ```

### Build Tools Integration

**Vite** (`vite.config.js`):
```js
import { defineConfig } from 'vite'
import compress from 'vite-plugin-compression'

export default defineConfig({
  plugins: [
    compress({ algorithm: 'gzip' }),
    compress({ algorithm: 'brotliCompress', ext: '.br' }),
  ],
})
```

**Next.js** (`next.config.js`):
```js
module.exports = {
  compress: false, // Let plumego handle compression
  output: 'export', // Static export
}
```

Then use a build script to pre-compress.

## Caching Strategy

### Recommended Approach

```go
frontend.WithCacheControl("public, max-age=31536000, immutable")
frontend.WithIndexCacheControl("no-cache, must-revalidate")
```

**Why?**
- **Assets** (`app.abc123.js`): Hash-based names → cache forever
- **Index** (`index.html`): No hash → don't cache, always fresh

### Build Tool Setup

Ensure your build tool generates hashed filenames:

**Vite**: Enabled by default
**Next.js**: Enabled by default
**Create React App**: Enabled by default

## Security Considerations

### Path Traversal Protection

Built-in protection against:
- `../` sequences
- Absolute paths
- Null bytes and backslash traversal forms
- Directory escapes

Directory-backed mounts created with `RegisterFromDir` and `http.Dir` inputs
passed to `RegisterFS` also reject symlink escapes outside the configured
frontend root. Other custom `http.FileSystem` implementations remain
responsible for their own backend storage boundaries.

Directory mounts resolve the root at construction time, so later process working
directory changes do not affect served assets.

### Method Restrictions

Only `GET` and `HEAD` requests are allowed. Other methods return
`405 Method Not Allowed` with `Allow: GET, HEAD`.

### Recommended Headers

```go
frontend.WithHeaders(map[string]string{
    "X-Frame-Options":         "DENY",
    "X-Content-Type-Options":  "nosniff",
    "Referrer-Policy":         "strict-origin-when-cross-origin",
    "Content-Security-Policy": "default-src 'self'",
    "Permissions-Policy":      "geolocation=(), microphone=(), camera=()",
})
```

## Advanced Examples

### Multiple Frontends

Serve different apps at different paths:

```go
// Main app at /
frontend.RegisterFromDir(r, "./main-app", frontend.WithPrefix("/"))

// Admin panel at /admin
frontend.RegisterFromDir(r, "./admin-app", frontend.WithPrefix("/admin"))

// Docs at /docs
frontend.RegisterFromDir(r, "./docs", frontend.WithPrefix("/docs"))
```

### API + Frontend

```go
// API routes
r.Get("/api/users", getUsersHandler)
r.Post("/api/users", createUserHandler)

// Frontend (must be registered last to avoid catching API routes)
frontend.RegisterFromDir(r, "./dist",
    frontend.WithPrefix("/"),
    frontend.WithFallback(true),
)
```

### Embedded with Fallback to Disk (Development)

```go
//go:embed dist/*
var embeddedFS embed.FS

func setupFrontend(r *router.Router) {
    // Prefer embedded production assets when present in this application.
    if subFS, err := fs.Sub(embeddedFS, "dist"); err == nil {
        if err := frontend.RegisterFS(r, http.FS(subFS)); err == nil {
            return
        }
    }

    // Fallback to disk for local development.
    if err := frontend.RegisterFromDir(r, "./dist"); err != nil {
        panic(err)
    }
}
```

Applications embed their own assets and pass the resulting `http.FileSystem` to
`RegisterFS`, as shown above.

## Performance Tips

1. **Use pre-compressed files** (`WithPrecompressed(true)`)
2. **Set long cache times for assets** (`max-age=31536000`)
3. **Don't cache index files** (`no-cache`)
4. **Use hashed filenames** (enabled by default in modern build tools)
5. **Embed assets in production** (single binary deployment)
6. **Combine with middleware.Gzip()** for dynamic responses

## Testing

The package includes comprehensive tests:

```bash
go test ./x/frontend/...
go test -bench=Benchmark -run '^$' ./x/frontend
```

See `frontend_test.go` for test examples covering:
- Pre-compressed file serving
- Custom error pages
- MIME type overrides
- Security (path traversal, unsafe backend opens, symlink escapes, method restrictions)
- Cache control behavior
- SPA navigation fallback and missing asset 404 behavior
- HTTP negotiation and custom error-page status behavior

## Compatibility

- **Go**: 1.26+ (as per plumego requirements)
- **Build Tools**: Any (Vite, Next.js, Create React App, etc.)
- **Browsers**: All modern browsers (gzip/brotli support)

## Related Packages

- `middleware.Gzip()`: Dynamic compression for API responses
- `frontend.RegisterFromDir()`: Explicit directory registration
- `frontend.RegisterFS()`: Explicit embedded filesystem registration

## License

See the main plumego repository for license information.
