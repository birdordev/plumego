# Card 1538 — limiter-admission

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: security
Owned Files: security/abuse/limiter.go, security/abuse/admission_regression_test.go, docs/modules/security/README.md, tasks/cards/done/1538-limiter-admission.md
Depends On: none

## Goal

Preserve existing limiter buckets under capacity pressure.

## Scope

security; only the listed defect, regression tests, and documentation.
Target module manifest read: security/module.yaml.

## Non-goals

No new public symbols, dependencies, unrelated cleanup, or changes to module ownership.

## Files

- security/abuse/limiter.go
- security/abuse/admission_regression_test.go
- docs/modules/security/README.md
- tasks/cards/done/1538-limiter-admission.md

## Impact and Ownership

The prior review reproduced this defect. Preserve successful existing behavior
while correcting failure/concurrency state. Mutable data ownership and resource
publication must remain explicit. Security-sensitive diagnostics stay sanitized.

## Acceptance Tests

security/abuse/admission_regression_test.go: TestFullLimiterPreservesExistingBucket

## Tests

Fixed clock repeated-key denial and concurrent multi-shard admission stay within MaxEntries.

## Docs Sync

docs/modules/security/README.md

## Validation

- `go test -race -timeout 60s ./security/...`
- `go vet ./security/...`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Regression tests pass and required state invariants are asserted.
- [x] Module checks and shared final gates pass.
- [x] Documentation matches implemented behavior.

## Outcome

Existing buckets keep their balance at capacity. New admissions serialize capacity changes; bucket count and its gauge use one atomic counter. Removed obsolete shard-count aggregation and eviction helper. Twenty repeated package race runs passed.

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

