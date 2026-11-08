# Card 1543 — requestid-initialization

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: middleware/requestid/generator.go, middleware/requestid/initialization_regression_test.go, middleware/requestid/README.md, tasks/cards/done/1543-requestid-initialization.md
Depends On: none

## Goal

Initialize zero-value request ID generators safely under concurrent first use.

## Scope

middleware; only the listed defect, regression tests, and documentation.
Target module manifest read: middleware/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- middleware/requestid/generator.go
- middleware/requestid/initialization_regression_test.go
- middleware/requestid/README.md
- tasks/cards/done/1543-requestid-initialization.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

middleware/requestid/initialization_regression_test.go: TestConcurrentZeroValueInitialization

## Tests

Concurrent first generation remains race-free and unique, including pool refresh.

## Docs Sync

middleware/requestid/README.md

## Validation

- `go test -race -timeout 60s ./middleware/...`
- `go vet ./middleware/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

sync.Once publishes complete zero-value initialization before pool reads. Concurrent first use across pool refresh verifies race freedom and ID uniqueness. Corrected stale UUID/accessor documentation.

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

