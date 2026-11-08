# Card 1539 — route-state-lifetime

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: router
Owned Files: router/dispatch.go, router/types.go, router/state_regression_test.go, docs/modules/router/README.md, tasks/cards/done/1539-route-state-lifetime.md
Depends On: none

## Goal

Keep routing metadata valid until all downstream work releases its request.

## Scope

router; only the listed defect, regression tests, and documentation.
Target module manifest read: router/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- router/dispatch.go
- router/types.go
- router/state_regression_test.go
- docs/modules/router/README.md
- tasks/cards/done/1539-route-state-lifetime.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

router/state_regression_test.go: TestRouteMetadataSurvivesInnerTimeout

## Tests

Cancel an inner timeout, then read route metadata after the router returns; exercise concurrent reads with race.

## Docs Sync

docs/modules/router/README.md

## Validation

- `go test -race -timeout 60s ./router/...`
- `go vet ./router/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

Routing metadata is initialized before cache publication and remains immutable. Contexts retain the match after cache eviction and timeout return. Cold/hot parameter, concurrent cancellation, and eviction regressions pass. Benchmarks retain three allocations for hot static/parameter routes; each cold match retains about 304 additional bytes.

Validated on 2026-11-08:
- Affected-module race regressions passed; the shared final `make gates`
  passed full race, boundary checks, stable API snapshots, references, CLI,
  coverage (95.8%), and generated website consistency.
- `go test -timeout 20s ./...` and `go vet ./...` passed on the final source.
- `make lint` passed with zero findings using CI-pinned golangci-lint v2.14.0
  installed under /tmp; no repository dependency or lint exclusion was added.
- `pnpm sync` in website passed; `git diff --check` passed. No website build
  was needed because no website source or dependency changed.

No public API additions or dependency changes. Original review reproducers
were kept outside the repository; focused regressions live next to their owning code.


Gate-selection follow-up: `make validate-diff-dry` reported `multi_module: true`
but selected `router_middleware_security` with router-only commands. This run
explicitly executed full `make gates`, whole-repo tests, vet, and lint instead.
The automatic selector needs a separate tooling correction before relying on it
for future cross-module changes; it did not limit this run's validation.
