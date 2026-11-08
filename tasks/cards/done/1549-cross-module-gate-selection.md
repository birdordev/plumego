# Card 1549 — cross-module-gate-selection

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: control-plane
Priority: P1
State: done
Primary Module: cmd/plumego
Owned Files: cmd/plumego/internal/gateprofile/gateprofile.go, cmd/plumego/internal/gateprofile/gateprofile_test.go, specs/gate-profiles.yaml, tasks/cards/done/1549-cross-module-gate-selection.md
Depends On: none

## Goal

Select repository-wide gates when a behavioral diff spans multiple modules.

## Scope

The CLI profile selector and its machine-readable selection rules.
Target module manifest: not applicable (CLI control-plane tooling).

## Non-goals

No runtime changes, module detection redesign, new profiles, API changes, or dependencies.

## Files

- cmd/plumego/internal/gateprofile/gateprofile.go
- cmd/plumego/internal/gateprofile/gateprofile_test.go
- specs/gate-profiles.yaml
- tasks/cards/done/1549-cross-module-gate-selection.md

## Impact and Ownership

Reproduced: a multi-module diff with a stable primary module selected its narrow
profile before reaching the cross-module rule. Move the cross-module rule before
stable rules and prevent stable rules from accepting multi-module input even if
configuration ordering regresses. Preserve documentation-only and single-module
selection. No runtime security or public API changes; no shared mutable state.

## Acceptance Tests

cmd/plumego/internal/gateprofile/gateprofile_test.go: TestSelectRepositoryProfiles
cmd/plumego/internal/gateprofile/gateprofile_test.go: TestSelectMultiModuleSkipsSingleModuleRules

## Tests

Load the real repository configuration. Cover stable/stable, stable/extension,
extension/extension, all stable primary modules under the old rule order,
single-module profiles, documentation-only changes, and full test/vet commands.

## Docs Sync

Selection precedence is documented in specs/gate-profiles.yaml. No generated
website inputs change.

## Validation

- `cd cmd/plumego && go test -race -timeout 60s ./... && go vet ./...`
- `go run ./internal/checks/agent-workflow`
- `make validate-diff`

## Done Definition

- [x] Acceptance tests fail before the fix and pass afterward.
- [x] CLI race tests and vet pass.
- [x] Current multi-module diff selects and passes repository-wide gates.
- [x] Diff formatting and workflow checks pass.

## Outcome

Moved cross-module selection ahead of stable-root rules. Stable-root matching
also rejects multi-module input, protecting scope even under the old rule order.
Both acceptance tests reproduced the defect before the fix and now pass.

Validated on 2026-11-08:
- CLI-wide race tests and vet passed.
- The workflow checker and git diff --check passed; touched Go files are formatted.
- make validate-diff-dry selected cross_module_public_api_release for the current
  multi-module diff. make validate-diff then passed all ten selected commands,
  including repository-wide race tests, ordinary tests, vet, and boundary checks.
- Targeted golangci-lint v2.14.0 passed with zero issues.

No public API or dependency changes. The older follow-up in card 1539 is resolved
by this card. Changes remain uncommitted.
