# Card 1542 — component-health-failure

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: x/observability/ops/healthhttp/handlers.go, x/observability/ops/healthhttp/component_regression_test.go, docs/modules/x/observability/ops/README.md, tasks/cards/done/1542-component-health-failure.md
Depends On: none

## Goal

Do not report cached component health as a successful fresh check.

## Scope

x/observability/ops; only the listed defect, regression tests, and documentation.
Target module manifest read: x/observability/ops/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- x/observability/ops/healthhttp/handlers.go
- x/observability/ops/healthhttp/component_regression_test.go
- docs/modules/x/observability/ops/README.md
- tasks/cards/done/1542-component-health-failure.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

x/observability/ops/healthhttp/component_regression_test.go: TestComponentHealthAfterCloseFailsClosed

## Tests

Closed, cancelled, missing, unhealthy, and superseded component checks must preserve safe HTTP semantics.

## Docs Sync

docs/modules/x/observability/ops/README.md

## Validation

- `go test -race -timeout 60s ./x/observability/ops/...`
- `go vet ./x/observability/ops/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

Component probes fail with 503 instead of replaying cached healthy status when closed, canceled, or superseded. Missing remains 404; backend text stays private and a newer shared result is not modified.

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

