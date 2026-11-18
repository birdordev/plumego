# x/tenant

## Purpose

`x/tenant` is the extension boundary for multi-tenant policy, quota, rate limit, resolution, and tenant-aware stores.

## v1 Status

- `beta` in the Plumego v1.1 support matrix
- Included in repository release scope with beta compatibility obligations for
  the documented public surface
- Promotion evidence is recorded in `docs/evidence/extension/x-tenant.md` with
  `v1.0.0` to `v1.1.0` release-backed API snapshots.

## Use this module when

- the task is tenant policy or isolation work
- the change is tenant-aware by design

## Do not use this module for

- stable middleware defaults
- stable store defaults
- generic request middleware unrelated to tenant semantics

## Main entrypoints

- `x/tenant/resolve` — tenant identity extraction and request-scoped resolution
- `x/tenant/policy` — allow/deny decisions and policy evaluation
- `x/tenant/quota` — usage budgeting and quota enforcement
- `x/tenant/ratelimit` — tenant-scoped rate limiting
- `x/tenant/config` — tenant configuration and management helpers
- `x/tenant/session` — session lifecycle and JWT-backed revocation/version state
- `x/tenant/store/*` — tenant-aware cache and database adapters

Use `session.NewJWTStateStoreWithConfig` with the same `jwt.JWTConfig`
as the JWT manager when configuring clock skew. `NewJWTStateStore` uses
the verifier's default skew. Revocation records expire at the end of the
effective validity window.

## First files to read

- `x/tenant/module.yaml`
- `docs/concepts/x-tenant-blueprint.md`
- the owning subpackage under `x/tenant/*`
- `AGENTS.md` tenant boundary rules

## Boundary rules

- keep tenant-aware logic out of stable `middleware` and stable `store`
- fail closed on resolution, policy, quota, and validation errors
- keep reference apps tenant-agnostic by default
- keep application-specific tenant CRUD outside this module

## Canonical change shapes

- resolution work starts in `x/tenant/resolve`
- deny-path, quota, and policy work starts in `x/tenant/core`, `policy`, `quota`, or `ratelimit`
- session lifecycle, tenant-session sentinel errors, and JWT-backed revocation/version work start in `x/tenant/session`
- tenant-aware persistence work starts in `x/tenant/store/*`, not in stable `store/*`
- tenant-owned configuration schema and migrations live under `x/tenant/config`

## Runnable resolution examples

- `x/tenant/example_test.go` shows the SaaS API starting point: explicit resolve → policy → quota → ratelimit middleware wiring with one allowed request and one fail-closed missing-tenant request
- `x/tenant/resolve/example_test.go` shows principal-first resolution and a custom extractor flow using query data instead of the default header path
- `x/tenant/transport/example_test.go` shows the canonical tenant transport headers for `Retry-After` and remaining quota state
- treat the `resolve` middleware order as explicit: principal first, then custom extractor, then configured tenant header fallback

## End-to-end middleware chain integration

`x/tenant/integration_test.go` (package `tenant_test`) covers the combined resolve → policy → quota → ratelimit middleware stack via `middleware.NewChain`. It verifies:

- full-pass: a correctly configured request reaches the handler (200)
- resolve fail: missing tenant identity is rejected at the resolve layer (401)
- policy deny: a disallowed model is rejected at the policy layer (403)
- quota exceeded: the second request after exhausting a 1-request-per-minute budget returns 429 with `Retry-After`
- rate limited: burst exhaustion returns 429 with `X-RateLimit-Limit`
- tenant isolation: exhausting tenant A's quota does not affect tenant B

## Tenant-aware store/db scope

- `x/tenant/store/db` is the tenant-aware SQL adapter layer; it does not widen stable `store/*`
- the adapter rewrites straightforward single-statement `SELECT`, `DELETE`, `UPDATE`, and `INSERT ... VALUES` queries to inject tenant scoping
- `QueryFromContext`, `ExecFromContext`, and `QueryRowFromContext` fail closed when tenant context is missing
- invalid tenant-column configuration is treated as a construction error and query helpers stay in an error state until fixed
- CTE-heavy SQL, `INSERT ... SELECT`, `UNION`, and other shapes that cannot be rewritten safely should use `RawDB()` with manual tenant filtering and optional `ValidateQuery(...)` checks

## Validation focus

- `go test -race -timeout 60s ./x/tenant/...`
- `go test -timeout 20s ./x/tenant/...`
- `go vet ./x/tenant/...`
- add negative-path coverage for isolation, quota exhaustion, and policy-deny behavior when changing public flows

Current example-backed and test-backed coverage includes:

- principal-first and custom-extractor resolution flows
- extractor error treated as missing tenant → fail closed (401) at the resolve layer
- `AllowMissing` opt-in passthrough and `OnMissing` custom callback
- resolve hook (`Hooks.OnResolve`) invoked with correct `TenantID` and `Source`
- invalid tenant ID format rejected at resolve (400) before policy/quota run
- unknown tenant (resolved but absent from config) denied at policy layer (403)
- HTTP method and URL path restriction in `ConfigPolicyEvaluator`, including trailing-`*` prefix patterns
- fail-closed tenant store/db scoping behavior and misconfiguration handling
- quota exhaustion with `Retry-After` and remaining-budget headers
- canonical policy-deny responses and tenant-scoped rate-limit isolation
- end-to-end middleware chain (resolve → policy → quota → ratelimit) including tenant isolation verification
- integration-level: extractor failure → 401; invalid format → 400; unknown tenant → 403

## Beta readiness

`x/tenant` satisfies the current coverage and boundary portions of
`docs/reference/extension-stability-policy.md`: resolution ordering, policy deny paths,
quota exhaustion, rate-limit isolation, tenant-aware store/db fail-closed
behavior, and the combined resolve → policy → quota → ratelimit chain have
focused tests.

The module is `beta` as of v1.1.0. Promotion evidence records unchanged
exported `x/tenant/*` APIs across `v1.0.0` to `v1.1.0`, release-backed
snapshots, and `multitenancy` owner sign-off. Resolution, policy, quota,
rate-limit, session, and tenant-aware store subpackages now carry beta
compatibility obligations for their documented public surface.
