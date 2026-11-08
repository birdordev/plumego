# Card 1562 — limiter-finite-rate

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: security/abuse/limiter.go, security/abuse/finite_regression_test.go, docs/modules/security/README.md, tasks/cards/done/1562-limiter-finite-rate.md
Depends On: none

## Goal

Reject non-finite limiter rates during configuration.

## Scope

Only the owned files. Target module manifest read: security/module.yaml.

## Non-goals

No stable public API changes, new dependencies, or unrelated cleanup.

## Files

- security/abuse/limiter.go
- security/abuse/finite_regression_test.go
- docs/modules/security/README.md
- tasks/cards/done/1562-limiter-finite-rate.md

## Impact and Ownership

The preceding review reproduced this failure. Preserve cancellation, panic,
and resource ownership. Route metadata is immutable and synchronized; ordinary
responses freeze headers at commitment. No credentials enter diagnostics.

## Acceptance Tests

security/abuse/finite_regression_test.go: TestFiniteRate

## Tests

Cover the reproduced negative path, successful behavior, and applicable
concurrent interleavings using channels rather than timing synchronization.

## Docs Sync

docs/modules/security/README.md

## Validation

- `go test -race -timeout 60s ./security/abuse/...`
- `go run ./internal/checks/agent-workflow`
- `GOMAXPROCS=4 make gates`

## Done Definition

- [x] Acceptance Tests pass.
- [x] All Validation commands exit 0.
- [x] Documentation and formatting match behavior.

## Outcome

Reject NaN and infinities with ErrInvalidConfig, preserve zero defaults and finite rates, and retain fail-closed convenience construction. Negative and finite configuration regressions passed.

Validated on 2026-11-08: owning-module race tests, repository-wide ordinary
and race tests, boundary/API/docs/reference/CLI gates, lint (zero findings),
and website sync passed. Stable-module coverage is 95.8%. Full acceptance used
GOMAXPROCS=4 without changing assertions or the 60-second timeout. Default
parallelism twice reached the CLI commands package timeout; an independent
original-parameter retry passed in 33.5 seconds and a fresh bounded-parallelism
CLI run passed in 48 seconds. The CLI runtime sensitivity remains a follow-up,
not a behavior change in this card. Generated website files remain unchanged.
No stable public API or dependency changes. Changes remain uncommitted.

