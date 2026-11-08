# Card 1540 — gzip-informational-status

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: middleware
Owned Files: middleware/compression/gzip.go, middleware/compression/informational_regression_test.go, middleware/compression/README.md, tasks/cards/done/1540-gzip-informational-status.md
Depends On: none

## Goal

Preserve final response status after informational headers.

## Scope

middleware; only the listed defect, regression tests, and documentation.
Target module manifest read: middleware/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- middleware/compression/gzip.go
- middleware/compression/informational_regression_test.go
- middleware/compression/README.md
- tasks/cards/done/1540-gzip-informational-status.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

middleware/compression/informational_regression_test.go: TestInformationalHeadersPreserveFinalResponse

## Tests

Multiple 100/103 responses followed by final success/error and compressed/plain output.

## Docs Sync

middleware/compression/README.md

## Validation

- `go test -race -timeout 60s ./middleware/...`
- `go vet ./middleware/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

100/103 responses no longer finalize gzip status or compression policy. Status-history recording and real HTTP traces verify final 201/404/500 status and intact response bodies. Plain httptest.ResponseRecorder treats the first informational response as final, so it is not used for wire-status assertions.

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

