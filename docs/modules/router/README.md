# router

## Purpose

`router` owns route matching, params, groups, and reverse routing.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this module when

- adding matching or grouping behavior
- changing path parameter extraction
- working on reverse routing or mount primitives
- registering routes directly with `Router.AddRoute(method, path, handler, opts...)`
- mounting a small static directory or `http.FileSystem` without frontend asset policy

## Do not use this module for

- JSON responses
- auth policy
- route parameter validation policy
- logger lookup or logger carriage
- service construction
- middleware ownership or middleware execution policy
- frontend asset policy such as cache headers, SPA fallback, precompressed assets, or ETag generation

## First files to read

- `router/module.yaml`
- `router/router.go`
- `router/registration.go`

## Public entrypoints

- `NewRouter`
- `MethodAny`
- `Router`
- `RouterOption`
- `RouteOption`
- `RouteMeta`
- `RouteInfo`
- `NamedRoute`
- `Group`
- `AddRoute`
- `Freeze`
- `HasRoute`
- `MethodNotAllowedEnabled`
- `NamedRoutes`
- `Param`
- `Print`
- `Routes`
- `ServeHTTP`
- `SetMethodNotAllowed`
- `URL`
- `URLMust`
- `WithRouteName`
- `WithMethodNotAllowed`
- `Static`
- `StaticFS`

## Main risks when changing this module

- dispatch regression
- param extraction regression
- reverse routing regression
- lifecycle ambiguity between mutable registration and immutable serving

## Canonical change shape

- preserve deterministic dispatch
- keep explicit method-plus-path registration behavior
- require registered route paths and group prefixes to be explicit
  leading-slash patterns, while repeated leading slashes collapse to one stored
  leading slash and internal empty segments remain invalid
- reject empty or invalid HTTP token methods, and keep route parameter names to
  unique ASCII identifiers such as `:id`, `:userID`, or `*rest_path`
- treat route registration failures as returned `error` values; do not model
  duplicate/conflict/frozen registration through panic-oriented helpers
- keep router APIs stdlib-shaped instead of alias-heavy
- use `net/http` method constants for standard HTTP methods
- keep one public param helper (`Param`)
- keep route metadata attached through `AddRoute(..., WithRouteName(...))`
- avoid bleeding response or middleware policy into router internals
- keep static serving as a small file-mount primitive; use `x/frontend` for frontend asset policy

## Lifecycle Contract

Direct `router.Router` callers own the build-and-serve boundary:

- register routes before serving traffic
- keep using the `*Router` returned by `NewRouter` or `Group`; Router values
  are not intended to be copied
- call `Freeze()` before serving when the runtime route table and router policy
  must be immutable
- do not call `AddRoute`, `Static`, `StaticFS`, or group registration while
  requests are concurrently being served
- treat `SetMethodNotAllowed` and direct `WithMethodNotAllowed(...)` option
  application after `Freeze()` as no-ops
- treat registration after `Freeze()` as a normal returned error, not a panic
  path
- lifecycle errors for uninitialized or frozen routers take precedence over
  route input validation errors
- `Static` and `StaticFS` follow the same lifecycle precedence before
  static prefix validation, filesystem resolution, or filesystem input work

`core.App` owns this boundary for app-managed routers. `core.App.Prepare()` and
the first `core.App.ServeHTTP(...)` path freeze the owned router before building
the handler chain, so callers using `core` do not call `Router.Freeze()`
directly.

## Boundary notes

- `router` does not own middleware registration or middleware chains.
- App-wide middleware belongs to `core.App.Use(...)` and the stable `middleware` package.
- Router stable imports are limited to stdlib plus `contract`; middleware
  integration stays outside this package.
- `router.MethodAny` is the reserved fallback method sentinel for wildcard
  method dispatch; it is not available as a separate exact custom HTTP method.
  Incoming `ANY` requests are treated like other uncommon methods and can be
  served by `MethodAny` fallback routes.
- Callers should prefer `core.App.Any(...)` for app-level catch-all routes.
- HEAD requests suppress response bodies for all matched routes while
  preserving handler-visible write counts; when no explicit HEAD route matches,
  HEAD can fall back to matching GET handlers.
- Route misses use the standard-library `http.NotFound` plain-text response.
- Nil or zero-value routers and nil requests are rejected with
  standard-library 503 plain-text `router not initialized` output.
- Router matching and match-cache keys are host-agnostic; dispatch is based on
  request method and normalized path.
- Requests with nil `URL` are rejected with standard-library 400 plain-text
  `bad request` output instead of panicking.
- When 405 handling is enabled, method mismatches use `contract.WriteError`
  with `method_not_allowed` and a sorted `Allow` header; a matching `GET`
  route also advertises implicit `HEAD`.
- Named route collisions fail registration; route names must be unique.
- `URL` consumes params as key/value pairs, percent-escapes segment params,
  preserves slash boundaries for wildcard params, returns empty string for
  unknown routes, incomplete params, duplicate keys, unknown keys, empty
  required params, or malformed key/value param lists; `URLMust` panics with the
  specific reverse-routing failure reason.
- `router.Static` and `router.StaticFS` are primitive GET mounts. Cache headers, SPA fallback, precompressed files, custom headers, and MIME policy belong to `x/frontend`.
- Static request paths are cleaned with slash-based URL semantics before local
  directory serving converts them to platform filesystem paths.
- Static mounts serve regular files only; directory requests return 404 and do
  not provide listing, index, or fallback behavior.
- Local static mounts resolve symlinks before opening files and reject resolved
  paths outside the static root at check time. They are intended for read-only
  or trusted roots; portable stdlib serving does not make concurrently mutated
  directories race-free.
- Static prefixes are canonicalized before registration: relative static
  prefixes gain a leading slash, trailing slashes are removed, and root mounts
  register as `/*filepath`.
- `Static` and `StaticFS` validate lifecycle first, then the normalized static
  route prefix, then filesystem-specific inputs. `Static` resolves and validates
  its local root during registration; missing roots and file roots fail fast.
- For embedded directories, pass a filesystem rooted at the mounted directory
  to `StaticFS`, for example `sub, _ := fs.Sub(public, "public")` followed by
  `r.StaticFS("/assets", http.FS(sub))`.

## Frozen behavior matrix

These behaviors are part of the current stable-root freeze baseline:

| Surface | Behavior |
| --- | --- |
| Registration | one method, one normalized path, one handler per route |
| Path normalization | route paths and group prefixes must start with `/` unless they are the empty group-root route; repeated leading slashes collapse, all trailing slashes are removed from non-root paths, and internal double slashes remain invalid |
| Params | route parameter names are unique per pattern; `Param(r, name)` and `contract.RequestContextFromContext` expose matched params |
| Groups | nested groups compose normalized prefixes and preserve named route metadata |
| Matching | static segments take precedence over params, and params take precedence over wildcards; warm cache preserves that result |
| Match cache | cache keys use method and normalized path; host does not participate in router matching. Successful registration invalidates cached matches, including when requests are served concurrently. |
| Reverse routing | `URL` percent-escapes params and returns empty string for unknown routes, missing params, empty params, duplicate keys, unknown keys, or malformed params; `URLMust` panics with the specific reason |
| Route snapshots | `Routes` returns method/path-sorted route metadata snapshots; uninitialized routers return non-nil empty `Routes` and `NamedRoutes` snapshots |
| 404 handling | unmatched routes use standard-library `http.NotFound` |
| Defensive request guard | nil request URL uses standard-library 400 plain-text `bad request` output |
| 405 handling | disabled by default; when enabled, returns sorted `Allow` including implicit `HEAD` for matching `GET` routes, plus canonical `contract` error body |
| HEAD fallback | HEAD suppresses response body writes for all matched routes and can use matching GET handlers when no explicit HEAD route exists |
| Freeze | Direct router users call `Freeze` before immutable serving; route registration fails and later runtime policy toggles, including direct option application, are ignored; `core.App` freezes owned routers during prepare/first serve |
| Static mounts | `Static` and `StaticFS` are small GET regular-file mounts, not frontend asset policy; local roots should be read-only or trusted for containment guarantees |

Focused regression coverage lives in `router/freeze_test.go`,
`router/router_contract_test.go`, `router/reverse_routing_group_test.go`, and
`router/static_test.go`. Lightweight seed-based fuzz coverage for path
normalization and reverse routing lives in `router/fuzz_test.go` and runs under
normal `go test`.

Router pool and match-cache helpers are internal hot-path details. Before
simplifying or expanding them, rerun the targeted benchmark set:
`go test -run '^$' -bench 'BenchmarkOpt(StaticRoute|ParamRoute|ParallelStatic)' -benchmem ./router`.
Cache key construction is intentionally simple string concatenation; rerun
`go test -run '^$' -bench BenchmarkOptCacheKey -benchmem ./router` before
adding pooling or other helper complexity.
