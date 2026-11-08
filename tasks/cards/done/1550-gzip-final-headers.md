# Card 1550 — gzip-final-headers

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: middleware
Owned Files: middleware/compression/gzip.go, middleware/compression/informational_regression_test.go, middleware/compression/README.md, tasks/cards/done/1550-gzip-final-headers.md
Depends On: none

## Goal

Keep informational headers isolated and commit final headers for empty responses.

## Scope

Only the owned paths. Target module manifest read: middleware/module.yaml.

## Non-goals

No public API changes, new dependencies, unrelated cleanup, or cross-module policy.

## Files

- middleware/compression/gzip.go
- middleware/compression/informational_regression_test.go
- middleware/compression/README.md
- tasks/cards/done/1550-gzip-final-headers.md

## Impact and Ownership

The preceding review reproduced gzip header pollution and lost empty-response
headers, and JWT candidate growth on failed activation. Deep metadata recursion
is source-confirmed. Only the defect owned by this card is in scope. Preserve
successful behavior and error classification. Shared state remains explicitly
owned; retries must retain reusable resources without publishing failed work.
No credentials enter diagnostics. No public API or dependency changes.

## Acceptance Tests

middleware/compression/informational_regression_test.go: TestInformationalHeaderIsolation
middleware/compression/informational_regression_test.go: TestEmptyResponseCommitsFinalHeaders

## Tests

Exercise the named failure path, recovery, and ownership guarantees; use an
isolated subprocess for stack-limit checks and real HTTP for informational responses.

## Docs Sync

middleware/compression/README.md

## Validation

- `go test -race -timeout 60s ./middleware/...`
- `go run ./internal/checks/agent-workflow`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Acceptance coverage passes and failure behavior is explicit.
- [x] Module race tests and shared gates pass.
- [x] Documentation and formatting match the implemented behavior.

## Outcome

Informational responses temporarily overlay a cloned header snapshot and restore the underlying map afterward. Deleted interim headers cannot poison final status/body framing. Header-only handlers commit final headers on normal completion; panic paths still leave uncommitted responses to outer recovery. Real HTTP regressions reproduced both defects before the fix and pass afterward, including repeated 103 responses, compressed success, uncompressed errors, outer headers, and empty responses.

Validated on 2026-11-08:
- Owning-module race tests passed, followed by the workflow check.
- Repository-wide ordinary tests and make gates passed. Full gates covered
  race tests, vet, references, CLI, boundaries, public API snapshots, snippets,
  stable-module coverage (95.8%), and generated website consistency.
- make lint passed with zero issues using CI-pinned golangci-lint v2.14.0.
- pnpm sync passed; no generated website diff was produced. Touched Go files
  are formatted and git diff --check passed.
- validate-diff-dry selected cross_module_public_api_release as expected.

No public API or dependency changes. Website build was not required because no
website source or dependency changed. This follow-up does not claim a new
whole-module audit. Changes are ready for review and remain uncommitted.

