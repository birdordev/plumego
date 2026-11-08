# Card 1552 — metadata-deep-clone

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: store
Owned Files: store/file/types.go, store/file/cycle_regression_test.go, docs/modules/store/README.md, tasks/cards/done/1552-metadata-deep-clone.md
Depends On: none

## Goal

Clone deep metadata graphs without growing the call stack while preserving cycles and ownership.

## Scope

Only the owned paths. Target module manifest read: store/module.yaml.

## Non-goals

No public API changes, new dependencies, unrelated cleanup, or cross-module policy.

## Files

- store/file/types.go
- store/file/cycle_regression_test.go
- docs/modules/store/README.md
- tasks/cards/done/1552-metadata-deep-clone.md

## Impact and Ownership

The preceding review reproduced gzip header pollution and lost empty-response
headers, and JWT candidate growth on failed activation. Deep metadata recursion
is source-confirmed. Only the defect owned by this card is in scope. Preserve
successful behavior and error classification. Shared state remains explicitly
owned; retries must retain reusable resources without publishing failed work.
No credentials enter diagnostics. No public API or dependency changes.

## Acceptance Tests

store/file/cycle_regression_test.go: TestCloneDeepMetadataWithBoundedStack

## Tests

Exercise the named failure path, recovery, and ownership guarantees; use an
isolated subprocess for stack-limit checks and real HTTP for informational responses.

## Docs Sync

docs/modules/store/README.md

## Validation

- `go test -race -timeout 60s ./store/...`
- `go run ./internal/checks/agent-workflow`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Acceptance coverage passes and failure behavior is explicit.
- [x] Module race tests and shared gates pass.
- [x] Documentation and formatting match the implemented behavior.

## Outcome

Metadata cloning uses a visited-container map and iterative worklist instead of recursive calls. An isolated subprocess with a 256 KiB stack reproduced the old stack overflow. The new implementation copies 20,000 alternating map/slice levels, preserves the leaf back-reference, and detaches leaf mutations. Existing cycle and alias regressions pass. Memory remains proportional to input size; custom values retain the documented caller ownership.

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

