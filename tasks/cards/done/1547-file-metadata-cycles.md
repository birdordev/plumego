# Card 1547 — file-metadata-cycles

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: store/file/types.go, store/file/cycle_regression_test.go, docs/modules/store/README.md, tasks/cards/done/1547-file-metadata-cycles.md
Depends On: none

## Goal

Clone cyclic file metadata without unbounded recursion or shared cloned containers.

## Scope

store; only the listed defect, regression tests, and documentation.
Target module manifest read: store/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- store/file/types.go
- store/file/cycle_regression_test.go
- docs/modules/store/README.md
- tasks/cards/done/1547-file-metadata-cycles.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

store/file/cycle_regression_test.go: TestCloneCyclicMetadata

## Tests

Self/cross-referencing maps and slices preserve graph identity inside a detached copy.

## Docs Sync

docs/modules/store/README.md

## Validation

- `go test -race -timeout 60s ./store/...`
- `go vet ./store/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

Visited-container tracking preserves self/cross references inside detached map[string]any and []any graphs. Both File.Clone and PutOptions.CloneMetadata avoid recursion on cycles and isolate input mutation.

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

