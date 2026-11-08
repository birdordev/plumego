# Compatibility and Migration Guide

This document explains how to upgrade Plumego, migrate from other frameworks, and prepare for future versions.

## Upgrading Plumego

### Within v1.x (e.g., v1.0 → v1.1 → v1.2)

**Patch releases (e.g., v1.1.0 → v1.1.1)** are always safe:
- Bug fixes only
- No API changes
- No deprecations

**Minor releases (e.g., v1.1.0 → v1.2.0)** are safe with caution:
- Stable root APIs do not break (v1 guarantee)
- Beta extensions may add features but will not break (deprecation notice required for removals)
- Experimental extensions may change APIs (check release notes)

To upgrade:

```bash
go get -u github.com/birdordev/plumego@v1.2.0
go mod tidy
go test ./...
```

### Major version (v1.x → v2.0)

**Not released yet.** When v2 is announced:

1. Read the migration guide in `docs/guides/migration/from-v1.md`
2. Check your code against the guide:
   - Rename or remove affected imports
   - Update handler signatures if they changed
   - Adjust middleware patterns if needed
3. Run tests: `go test -vet=all ./...`
4. Update your go.mod: `go get github.com/birdordev/plumego@v2.0.0`
5. Incrementally migrate (can often do one package at a time)

The migration guide will include:
- Specific breaking changes and why
- Find-and-replace patterns where possible
- Step-by-step examples
- Timeline for v2 support

## Migrating from other frameworks

If you're using another Go web framework (Gin, Echo, Chi), Plumego offers a clearer, more maintainable path. Choose the guide that matches your current framework:

### From Gin

See `docs/guides/migration/from-gin.md`

**Key differences:**
- Gin handlers return `(w, r)`; you call `c.JSON()`, `c.Error()`. Plumego handlers return nothing; you call `contract.WriteResponse()`, `contract.WriteError()`.
- Gin uses `c.Param("id")`. Plumego uses `contract.Param(r, "id")` via request context.
- Gin middleware receives `*gin.Context`. Plumego middleware wraps `http.Handler` and works with stdlib patterns.

**Migration path:**
1. Read the Gin migration guide
2. Replace `gin.Engine` with `core.New()`
3. Convert handlers to use `contract.WriteResponse/WriteError`
4. Convert middleware to standard library `http.Handler` pattern
5. Test incrementally

### From Echo

See `docs/guides/migration/from-echo.md`

**Key differences:**
- Echo handlers are `func(c echo.Context) error`. Plumego handlers are `func(w http.ResponseWriter, r *http.Request)`.
- Echo's middleware is `func(echo.HandlerFunc) echo.HandlerFunc`. Plumego's is standard `http.Handler` wrappers.
- Echo groups manage routes; Plumego's router groups are explicit.

**Migration path:**
1. Read the Echo migration guide
2. Replace `echo.New()` with `core.New()`
3. Convert handlers to take `(http.ResponseWriter, *http.Request)`
4. Use `contract.Param(r, "id")` instead of `c.Param("id")`
5. Convert middleware to `func(http.Handler) http.Handler` pattern

### From Chi

See `docs/guides/migration/from-chi.md`

**Key differences:**
- Chi and Plumego are both stdlib-compatible, so migration is smallest
- Plumego's middleware composition is similar to Chi but more explicit
- Plumego adds `contract.WriteResponse/WriteError` for structured responses

**Migration path:**
1. Read the Chi migration guide
2. Replace `chi.NewMux()` with `core.New()`
3. Keep handlers as-is (they're already stdlib-compatible)
4. Optionally adopt `contract.WriteResponse` for structured responses
5. Test incrementally

### From plain stdlib http.ServeMux

See `docs/guides/migration/from-stdmux.md`

**Key differences:**
- stdlib `ServeMux` has basic routing; Plumego adds path params, groups, and metadata
- stdlib requires manual request binding; Plumego provides `contract` helpers
- stdlib responses are manual; Plumego provides structured response helpers

**Migration path:**
1. Read the stdlib migration guide
2. Replace manual `mux.HandleFunc()` registration with `app.Get()`, `app.Post()`, etc.
3. Use `contract.Param(r, "id")` instead of manual path parsing
4. Use `contract.WriteResponse()` instead of manual `json.NewEncoder(w).Encode(data)`
5. Test incrementally

## Dependency stability

### Stable roots have zero external dependencies

All 9 stable root packages (`core`, `router`, `contract`, `middleware`, `security`, `store`, `health`, `log`, `metrics`) depend only on the Go standard library. This is a v1 guarantee.

- ✅ Safe for long-term projects
- ✅ No dependency conflicts
- ✅ No supply chain risk from external updates
- ✅ Auditable with `go vet ./...`

### Beta extensions may have external dependencies

Beta extensions like `x/observability` (for OpenTelemetry) may import external packages, but only as optional dependencies:

- The extension is in a separate package (`x/observability`)
- You choose to import it or not
- If you don't use it, no external deps are pulled

### Experimental extensions may have experimental dependencies

Experimental extensions may depend on unstable or early-stage libraries. Use at your own risk.

To check what your app depends on:

```bash
go list -u -m all
go mod graph | grep spcent/plumego
```

## Future compatibility

### When Plumego v2 is released

- **Timeline:** TBD, likely 12-18 months from v1.0
- **Will include:** Performance improvements, API simplifications, dropped Go version support (will require Go 1.28+)
- **Migration:** Step-by-step guide will be provided
- **Support:** v1.x will receive bug fixes for 6 months after v2 release

### Breaking changes expected in v2

Based on current design, we expect:

- ❌ Handler signature changes (unlikely, stable design)
- ❌ Middleware pattern changes (unlikely, stdlib-compatible)
- ✅ Removal of deprecated symbols
- ✅ Minimum Go version bump (1.28+)
- ✅ Possible renames for clarity
- ✅ Simplifications to remove rarely-used APIs

Specifics will be announced 6 months before v2 release.

## Go version support

| Plumego | Minimum Go | Maximum Go | LTS |
|---------|------------|-----------|-----|
| v1.0.x  | 1.26       | any       | No  |
| v1.1.x  | 1.26       | any       | Yes (12 months) |
| v1.2.x+ | 1.26       | TBD       | Per release |
| v2.0+   | 1.28+      | any       | TBD |

We support the **two most recent Go releases** as a minimum, and often support older versions if the effort is low.

To check compatibility:

```bash
go version
go mod why github.com/birdordev/plumego
```

## Security updates

See `SECURITY.md` for the vulnerability reporting process and supported versions.

- **Active versions** receive security patches
- **Maintenance versions** receive critical security patches only
- **Unsupported versions** do not receive updates

## Deprecation notices

When an API is deprecated, the release notes and code will clearly indicate:

1. **What is changing:** The symbol name and its replacement
2. **Why it's changing:** Brief explanation of the improvement
3. **Timeline:** When it will be removed
4. **How to update:** Step-by-step migration instructions

Example:

```
v1.3.0 DEPRECATION: core.NewWithoutDefaults() is deprecated.
Use core.New(core.DefaultConfig(), core.AppDependencies{}) instead.
This will be removed in v1.5.0 or later.
See COMPATIBILITY.md for details.
```

Deprecated symbols are marked in code:

```go
// Deprecated: use core.New(core.DefaultConfig(), core.AppDependencies{}) instead.
// This function will be removed in v1.5.0.
func NewWithoutDefaults() *App { ... }
```

## Testing compatibility

When you upgrade Plumego or another dependency:

```bash
# Run all tests
go test ./...

# Check for vet issues
go vet ./...

# Check for deprecated calls (Go 1.27+)
go list -deprecated ./...

# Build and ensure no import errors
go build ./...

# Run with race detector if tests are concurrent
go test -race ./...
```

## Questions or issues?

- **Upgrade question:** Open an issue with your current version and target version
- **Migration question:** Check the framework-specific guide; open an issue if unclear
- **Deprecation timeline:** Check `docs/release/ROADMAP.md`
- **Security concern:** See `SECURITY.md`

---

For detailed API stability policy, see `STABILITY.md`.  
For extension maturity levels, see `docs/concepts/extension-maturity.md`.
