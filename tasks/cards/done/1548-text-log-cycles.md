# Card 1548 — text-log-cycles

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: log/logger.go, log/text_cycle_regression_test.go, docs/modules/log/README.md, tasks/cards/done/1548-text-log-cycles.md
Depends On: none

## Goal

Format common cyclic text log containers with a bounded traversal.

## Scope

log; only the listed defect, regression tests, and documentation.
Target module manifest read: log/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- log/logger.go
- log/text_cycle_regression_test.go
- docs/modules/log/README.md
- tasks/cards/done/1548-text-log-cycles.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

log/text_cycle_regression_test.go: TestTextCyclicFieldsAreBounded

## Tests

Cyclic maps/slices, depth/node budgets, safe sibling fields, and unchanged scalar formatting.

## Docs Sync

docs/modules/log/README.md

## Validation

- `go test -race -timeout 60s ./log/...`
- `go vet ./log/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

Text rendering bounds reflected containers, pointers, and struct fields before fmt can recurse. Scalar/time formatting and safe sibling fields remain intact. Custom Formatter/Stringer/error implementations retain ownership of their termination.

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

