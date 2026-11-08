# Card 1551 — jwt-pending-rotation

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: security/jwt/jwt.go, security/jwt/keys.go, security/jwt/rotation_regression_test.go, docs/modules/security/README.md, tasks/cards/done/1551-jwt-pending-rotation.md
Depends On: none

## Goal

Reuse a recoverable signing-key candidate across failed activation retries and restart.

## Scope

Only the owned paths. Target module manifest read: security/module.yaml.

## Non-goals

No public API changes, new dependencies, unrelated cleanup, or cross-module policy.

## Files

- security/jwt/jwt.go
- security/jwt/keys.go
- security/jwt/rotation_regression_test.go
- docs/modules/security/README.md
- tasks/cards/done/1551-jwt-pending-rotation.md

## Impact and Ownership

The preceding review reproduced gzip header pollution and lost empty-response
headers, and JWT candidate growth on failed activation. Deep metadata recursion
is source-confirmed. Only the defect owned by this card is in scope. Preserve
successful behavior and error classification. Shared state remains explicitly
owned; retries must retain reusable resources without publishing failed work.
No credentials enter diagnostics. No public API or dependency changes.

## Acceptance Tests

security/jwt/rotation_regression_test.go: TestFailedRotationsReuseCandidate
security/jwt/rotation_regression_test.go: TestRotationCandidateSurvivesRestart

## Tests

Exercise the named failure path, recovery, and ownership guarantees; use an
isolated subprocess for stack-limit checks and real HTTP for informational responses.

## Docs Sync

docs/modules/security/README.md

## Validation

- `go test -race -timeout 60s ./security/...`
- `go run ./internal/checks/agent-workflow`
- `make gates` (shared final acceptance)

## Done Definition

- [x] Acceptance coverage passes and failure behavior is explicit.
- [x] Module race tests and shared gates pass.
- [x] Documentation and formatting match the implemented behavior.

## Outcome

A manager retains one pending rotation candidate until blob persistence and active-pointer publication are acknowledged. Rotation ancestry is stored in the existing key blob and recovered on restart without changing the public key type or KeyStore interface. Regressions reproduced repeated-failure growth and restart replacement before the fix. Manual/automatic retries, blob/pointer failures, and lost acknowledgments now reuse the same candidate. Old signing keys and legacy blobs remain readable. Missing active markers preserve fresh-key recovery; no transaction or distributed coordination guarantee is added.

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

