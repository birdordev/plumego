# Card 1553 — route-state-lifetime-doc

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: contract
Owned Files: contract/context_core.go, tasks/cards/done/1553-route-state-lifetime-doc.md
Depends On: none

## Goal

Align WithRouteState documentation with immutable router-owned state and explicit scratch ownership.

## Scope

Only the owned paths. Target module manifest read: contract/module.yaml.

## Non-goals

No public API changes, new dependencies, unrelated cleanup, or cross-module policy.

## Files

- contract/context_core.go
- tasks/cards/done/1553-route-state-lifetime-doc.md

## Impact and Ownership

The preceding review reproduced gzip header pollution and lost empty-response
headers, and JWT candidate growth on failed activation. Deep metadata recursion
is source-confirmed. Only the defect owned by this card is in scope. Preserve
successful behavior and error classification. Shared state remains explicitly
owned; retries must retain reusable resources without publishing failed work.
No credentials enter diagnostics. No public API or dependency changes.

## Acceptance Tests

Documentation-only clarification; existing lifecycle regressions remain authoritative.

## Tests

Exercise the named failure path, recovery, and ownership guarantees; use an
isolated subprocess for stack-limit checks and real HTTP for informational responses.

## Docs Sync

contract/context_core.go

## Validation

- `go test -race -timeout 60s ./contract/... ./router/...`
- `go run ./internal/checks/agent-workflow`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Acceptance coverage passes and failure behavior is explicit.
- [x] Module race tests and shared gates pass.
- [x] Documentation and formatting match the implemented behavior.

## Outcome

WithRouteState now documents immutable router metadata retained by contexts beyond ServeHTTP, while manual callers own synchronization and must delay reset/pool reuse until every reader has finished. GetRouteState documents the same ownership rule. No behavior or public symbol changes were made; existing contract/router lifecycle race tests pass.

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

