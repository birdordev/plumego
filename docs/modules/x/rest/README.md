# x/rest

## Purpose

`x/rest` is Plumego's app-facing reusable resource-interface layer for CRUD route standardization, query parsing, pagination rules, and repository-backed resource wiring.

## v1 Status

- `beta` in the Plumego v1 support matrix
- Included in repository release scope with beta compatibility obligations
- Promoted at `v0.2.0` after release-backed evidence showed no exported
  `x/rest` API changes across refs `d2c25c3` and `ec70358`, with
  `platform-api` owner sign-off recorded in `docs/evidence/extension/x-rest.md`

## Use this module when

- the task is standardizing reusable resource APIs
- the change is controller-level CRUD transport behavior
- the change is shared query, pagination, hook, or transformer behavior for resource endpoints

## Do not use this module for

- application bootstrap
- gateway or reverse-proxy topology
- business repository ownership
- domain validation that belongs in the owning package

## First files to read

- `x/rest/module.yaml`
- `x/rest/spec.go`
- `x/rest/entrypoints.go`
- `reference/standard-service` when checking canonical bootstrap shape

## Public entrypoints

- `ResourceSpec`
- `RegisterResourceRoutes`
- `NewDBResource`
- `RegisterDBResource`

## Main risks when changing this module

- non-canonical resource route registration
- response or error drift away from `contract`
- hidden repository injection or implicit bootstrap behavior
- controller defaults diverging across services

## Canonical change shape

- keep route binding explicit in app-local wiring
- keep reusable CRUD transport behavior in `x/rest`
- keep response and error conventions aligned with `contract`
- keep domain validation and business rules outside `x/rest`

## Recommended layering

1. Define a `ResourceSpec`
2. Build a repository implementation
3. Create a `DBResourceController` or custom context-aware controller
4. Register routes explicitly in an app-local wiring package

Recommended ownership split:

- app-local wiring: owns route binding and transport composition
- `x/rest`: owns reusable CRUD transport behavior
- repository: owns persistence and query execution
- domain package: owns business validation and business rules

## Canonical example

The runnable example in `x/rest/example_test.go` shows the same pattern with an
in-memory repository, explicit router registration, and a real `httptest`
request through the registered route.

```go
package users

import (
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/rest"
)

func RegisterRoutes(r *router.Router, repository rest.Repository[User]) {
	spec := rest.DefaultResourceSpec("users").WithPrefix("/api/users")

	controller := rest.NewDBResource(spec, repository)
	if err := rest.RegisterResourceRoutes(r, spec.Prefix, controller, rest.DefaultRouteOptions()); err != nil {
		panic(err)
	}
}
```

## Response and error guidance

- keep using `contract.WriteError` for structured error responses
- keep using contract-based response helpers for transport output
- default resource controllers should write errors directly through `contract`, not through `x/rest`-local response wrappers
- `DBResourceController` default error responses use stable contract codes and safe public messages; hook, repository, and transformer internals are not exposed through default client-facing messages
- do not introduce a separate `x/rest`-specific response envelope family
- treat `x/rest` as reusable controller and route-binding infrastructure, not as a replacement transport contract layer

## Boundary rules

- `x/rest` is a transport-layer library; it does not own persistence, domain validation, or application bootstrap
- keep route binding explicit in app-local wiring; do not register routes automatically at import time
- keep response and error conventions aligned with `contract`; do not introduce `x/rest`-local error envelopes
- keep domain validation and business rules outside `x/rest`
- `RegisterResourceRoutes` returns explicit errors for nil router or controller so app wiring mistakes are visible; empty prefixes normalize to the root route surface
- `x/rest` is not an ORM — `SQLBuilder` is a query helper, not an active-record layer; column-level SQL safety (filter-key interpolation) depends on `QueryBuilder.WithAllowedFilters` being configured; a `QueryBuilder` without an allowlist passes all filter keys through to SQL
- `x/rest` is not a validation framework — `DBResourceController.WithValidator` accepts any `interface{ Validate(any) error }`; the validator's error message is never forwarded to the client; only the stable "validation failed" message is exposed
- `x/rest` does not own route discovery or auto-registration; every resource must be wired explicitly by the calling package

## Current test coverage

- `RegisterResourceRoutes`: canonical route surface (all 12 routes), `RouteOptions` selective enable/disable, `BaseContextResourceController` wiring, nil router and nil controller errors, empty prefix normalization
- `RegisterDBResource`: end-to-end registration builds controller, registers routes, and serves a real request
- `DefaultRouteOptions`: all three flags enabled
- `DefaultResourceSpec`: canonical name and prefix assignment, `WithPrefix` override
- `BaseResourceController`: all 8 not-implemented methods (Index, Show, Create, Update, Delete, Patch, BatchCreate, BatchDelete) return HTTP 501 with `contract.CodeNotImplemented`; `Options` sets CORS headers and returns 204; `Head` returns 200; empty `ResourceName` defaults to `"resource"`
- `DBResourceController.Index`: canonical contract response shape (`{"data": [...], "meta": {"pagination": {...}}}`), repository errors use stable messages, hook errors use safe codes
- `DBResourceController.Show`: empty ID returns 400 `CodeRequired`; non-`ErrNoRows` repository error returns 500 `CodeInternalError` with internal message omitted; `sql.ErrNoRows` returns 404 `CodeResourceNotFound`
- `DBResourceController.Create`: invalid JSON returns 400 `CodeInvalidRequest`; repository error returns 500 with internal message omitted; validation error returns 422 `CodeValidationError`; `BeforeCreate` hook rejection returns 4xx (not 5xx) with hook message omitted
- `DBResourceController.Update`: empty ID returns 400 `CodeRequired`; `sql.ErrNoRows` returns 404; `BeforeUpdate` hook rejection returns 4xx with hook message omitted
- `DBResourceController.Delete`: empty ID returns 400 `CodeRequired`; `sql.ErrNoRows` returns 404; success returns 204; `BeforeDelete` hook rejection returns 4xx with hook message omitted
- `ApplyResourceSpec` / nil controller: nil controller is a no-op (no panic)
- `BaseContextResourceController.ParseQueryParams`: nil receiver returns a safe default (no panic)
- `DBResourceController.ApplySpec`: nil receiver is a no-op (no panic)
- `NewDBResource`: empty `ResourceSpec{}` normalizes safely (name defaulted, controller non-nil)
- `ResourceSpec` / `ApplyResourceSpec`: controller defaults preservation, spec-driven query normalization, legacy sort field filtering
- `NewPaginationMeta`: first-page (HasPrev=false, HasNext=true), last-page (HasNext=false), zero-item (TotalPages=0, no navigation), zero/negative pageSize returns without panic, single-exact-page (HasNext=false, HasPrev=false), exact divisor (no extra page)
- `QueryBuilder`: page-size clamping to max; invalid, zero, and negative page and page_size input use defaults; unknown sort field filtered out; bare-dash sort (`-`) filtered out (no empty-field `SortField`); unknown filter field filtered out; descending sort prefix (`-`) parsing; `q=` fallback when `search=` is absent; `search=` takes precedence over `q=`; `fields=` and `include=` comma list parsing; explicit `offset=` respected

## Beta readiness

`x/rest` satisfies the current coverage and boundary portions of
`docs/reference/extension-stability-policy.md`: documented route registration,
controller defaults, query parsing, pagination, nil-receiver safety, hook and
transformer error paths, invalid-argument rejection, and not-implemented negative
paths all have focused tests.

Two fixes applied during production-trust hardening:
- `NewPaginationMeta` now guards against `pageSize ≤ 0` to prevent division-by-zero
- `QueryBuilder.Parse` now filters bare-dash sort inputs (`sort=-`) that produced an empty-field `SortField` when no sort allowlist was configured

The module is beta. The beta evidence in `docs/evidence/extension/x-rest.md`
records two release refs, matching API snapshots, no exported API changes, and
`platform-api` owner sign-off.

## Agent guidance

- If the task is "standardize CRUD/resource API shape", start here
- If the task is "proxy/edge transport", start in `x/gateway`
- If the task is "bootstrap a service", start in `reference/standard-service`
- If the task is "domain validation or business rules", keep that logic outside `x/rest`
- If the task is "wire CRUD + request validation + OpenAPI docs together", see `docs/concepts/rest-api-composition.md`
