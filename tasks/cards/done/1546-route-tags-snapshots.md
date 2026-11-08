# Card 1546 — route-tags-snapshots

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: router/metadata.go, router/registration.go, router/tags_regression_test.go, docs/modules/router/README.md, tasks/cards/done/1546-route-tags-snapshots.md
Depends On: none

## Goal

Detach mutable route tags from registration inputs and returned snapshots.

## Scope

router; only the listed defect, regression tests, and documentation.
Target module manifest read: router/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- router/metadata.go
- router/registration.go
- router/tags_regression_test.go
- docs/modules/router/README.md
- tasks/cards/done/1546-route-tags-snapshots.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

router/tags_regression_test.go: TestRouteTagsAreDetached

## Tests

Modify registration metadata and output tags, then read frozen route snapshots concurrently.

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

Registration copies option-provided tags; Routes copies each returned tag slice. Mutating inputs or snapshots leaves frozen routing metadata unchanged under concurrent reads.

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

