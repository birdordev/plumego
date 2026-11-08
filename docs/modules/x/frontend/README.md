# x/frontend

## Purpose

`x/frontend` provides explicit helpers for static asset serving, embedded frontend mounts, and SPA fallback behavior.

## v1 Status

- `beta` in the Plumego v1.1 support matrix
- Included in repository release scope with beta compatibility obligations for
  the documented public surface
- Hardened coverage exists for path safety, symlink escape rejection,
  precompressed negotiation, cache variance, custom page policy, navigation-only
  fallback, and basic mount behavior. Basic benchmarks cover normal and
  precompressed asset serving.
- Release-backed API snapshot evidence and `frontend` owner sign-off are
  recorded in `docs/evidence/extension/x-frontend.md`.

## Use this module when

- the task is static asset serving
- the task is embedded frontend mounting
- the task is fallback behavior for single-page applications
- the task needs frontend asset policy such as cache headers, custom headers, precompressed assets, or MIME overrides

## Do not use this module for

- application bootstrap
- business-specific UI flow logic
- hidden filesystem registration in `core`
- route matching primitives; keep those in `router`

## First files to read

- `x/frontend/module.yaml`
- `x/frontend/README.md`
- `specs/task-routing.yaml`

## Main risks when changing this module

- hidden filesystem side effects
- process working-directory changes affecting directory-backed mounts
- fallback regression
- missing asset requests accidentally returning SPA index responses
- directory symlink escape regression
- `net/http` incompatibility in mounting behavior
- precompressed response cache variance regression

## Canonical change shape

- keep frontend mounting explicit
- preserve transport-only helper behavior
- do not let frontend helpers redefine the canonical app path
- own frontend asset policy here instead of adding policy knobs to stable `router`

## Boundary with bootstrap

- `x/frontend` is a secondary capability root for asset serving, not an application bootstrap surface
- read `reference/standard-service` only to align app shape and route wiring
- keep frontend registration explicit in app-local code

## Boundary with router

- `router.Static` and `router.StaticFS` are small stable file-mount primitives.
- `x/frontend` owns higher-level frontend serving behavior: SPA fallback, cache headers, custom headers, precompressed files, custom error pages, and MIME overrides.
- Do not add frontend asset policy knobs back to stable `router`; add or refine them here.

## Registration Contract

- Root mounts register ANY `/` and ANY `/*filepath`.
- Prefixed mounts register ANY `<prefix>/*filepath` and ANY `<prefix>`.
- Registrars that expose route snapshots are preflighted for duplicate target
  routes before mutation, so duplicate target routes are rejected before any
  frontend route is added.
- AddRoute-only custom registrars remain best-effort sequential targets. They
  do not get rollback, and a failure on a later frontend route may leave earlier
  frontend routes registered.
- Stable callers that need duplicate-route atomicity should use `router.Router`
  or another registrar that exposes snapshots.
- `Mount.Prefix` returns an empty string for a nil mount, and `Mount.Handler`
  returns nil for a nil mount. `Mount.Register` rejects nil mounts and nil
  registrars.

## Header Policy

- Use `WithHeaders` for security and metadata headers only.
- Use dedicated options for cache, MIME, and precompressed response semantics.
- `WithCacheControl`, `WithIndexCacheControl`, `WithMIMETypes`, and
  `WithHeaders` reject unsafe header values containing CR, LF, NUL, or other
  control characters during mount construction.
- Map-backed options copy caller-provided maps when the exported helper is
  created. Later caller mutations do not affect the mount.
- `WithMIMETypes` accepts single file extension keys with or without a leading
  dot. Empty keys, path-like keys, multi-extension keys, whitespace, and control
  characters are rejected because response matching uses `path.Ext`.
- Do not allow caller-provided headers to override internally managed
  `Content-*`, `Vary`, cache, conditional, range, or hop-by-hop response
  semantics.

## Encoding Negotiation

- `x/frontend` serves precompressed `.br` and `.gz` variants only when requested
  and available.
- Directory-backed mounts, including `http.Dir` inputs passed to `RegisterFS`,
  build immutable precompressed variant metadata at construction time and fail
  mount construction when the variant scan hits filesystem errors.
- Missing or unreadable compressed variants are treated as best-effort misses:
  the original asset is served when `identity` is acceptable, while requests
  that refuse `identity` still receive `406 Not Acceptable`.
- `WithPrecompressedVariantMissHandler` provides application-owned observability
  for planned variant open misses and accepted variant stat misses. By default,
  x/frontend emits no log or metric and keeps best-effort downgrade behavior.
- Non-`http.Dir` caller-provided filesystems keep lazy variant probing. Original
  responses may open `.br` and `.gz` candidates to decide
  `Vary: Accept-Encoding`; prefer directory-backed mounts when that backend
  probing cost is too high.
- Custom filesystems that already have reliable variant metadata can provide
  `WithPrecompressedVariantPlan` to avoid lazy `.br`/`.gz` miss probes on
  original responses. Callers own the correctness of the supplied plan.
- Invalid `Accept-Encoding` quality values invalidate that token rather than
  being clamped.
- Requests that refuse `identity` receive `406 Not Acceptable` when no accepted
  precompressed variant can be served.

## Filesystem Contract

- Directory mounts and `http.Dir` inputs resolve the configured root to an
  absolute canonical path at construction time.
- Directory mounts and `http.Dir` inputs fail fast when the configured index
  file is missing or is a directory.
- Directory-backed assets are treated as static deployment artifacts. Variant
  metadata is captured at mount construction time and does not provide a
  runtime atomic snapshot if files are mutated in place.
- Non-`http.Dir` caller-provided `http.FileSystem` implementations remain lazy
  and own their backend readiness and storage boundaries.

## Public API Freeze Candidates

`x/frontend` is not promoted yet, but these exported names are the surface that
must be compared with release-backed snapshots before stable adoption:

- `Registrar`
- `Mount`, including `Prefix`, `Handler`, and `Register`
- `RegisterFromDir` and `RegisterFS`
- `NewMountFromDir`, `NewMountFS`, and `NewHandlerFS`
- sealed `Option`
- `WithPrefix`, `WithIndex`, `WithCacheControl`, `WithIndexCacheControl`,
  `WithFallback`, `WithHeaders`, `WithPrecompressed`, `WithNotFoundPage`,
  `WithErrorPage`, `WithMIMETypes`, `WithPrecompressedVariantMissHandler`, and
  `PrecompressedVariantMiss`, `WithPrecompressedVariantPlan`, and
  `PrecompressedVariants`

Treat `Option` as an intentionally sealed constructor input. The package-private
config type is not a caller extension point; future stable configuration should
arrive as explicit exported `With*` helpers.

## Stable-readiness blockers

- `status` remains `experimental`; do not promote without owner approval.
- The option API is intentionally sealed: callers compose exported `With*`
  options, and new stable knobs should be added as explicit helpers rather than
  caller-defined custom options against internal config state.
- Public API compatibility still needs a release snapshot comparison for
  `Registrar`, `Mount`, `Option`, `NewMountFromDir`, `NewMountFS`,
  `NewHandlerFS`, `RegisterFromDir`, `RegisterFS`, and the `With*` options.
- Current-head snapshot:
  `docs/evidence/extension/snapshots/first-batch/x-frontend-head.snapshot`.
- Current-head snapshots are development evidence only; they do not clear the
  release-backed `api_snapshot_missing` blocker.
- Release evidence must show at least two consecutive minor releases without
  exported API churn.
- Stable promotion also needs a passing repository release gate from the
  candidate release state; targeted `x/frontend` checks are not enough.
- Embedded helper guidance must stay explicit: applications pass their own
  `embed.FS`/`fs.Sub` result through `RegisterFS` as `http.FS`; examples should
  not present `http.Dir` as the embedded-filesystem path.

## Security

Directory-backed files are opened through `os.OpenInRoot` to prevent symlink escapes during file opening. Precompressed original checks use the same entrypoint. Deployments must control the root directory and its parents.
