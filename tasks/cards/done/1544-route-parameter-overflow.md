# Card 1544 — route-parameter-overflow

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: contract
Owned Files: contract/context_core.go, contract/accessors_test.go, router/state_regression_test.go, docs/modules/contract/README.md, tasks/cards/done/1544-route-parameter-overflow.md
Depends On: none

## Goal

Preserve parameters beyond inline route-state capacity.

## Scope

contract; only the listed defect, regression tests, and documentation.
Target module manifest read: contract/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- contract/context_core.go
- contract/accessors_test.go
- router/state_regression_test.go
- docs/modules/contract/README.md
- tasks/cards/done/1544-route-parameter-overflow.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

contract/accessors_test.go: TestRouteStatePreservesOverflowParameters

## Tests

Inline and overflow accessors, detached request snapshots, and reset remove all old parameter references.

## Docs Sync

docs/modules/contract/README.md

## Validation

- `go test -race -timeout 60s ./contract/...`
- `go vet ./contract/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

Overflow parameters are retained outside the eight inline slots. Accessors include all values, detached snapshots cannot mutate the state, and Reset releases overflow storage. Router entrypoint tests cover nine parameters plus a wildcard with cold/hot cache.

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

