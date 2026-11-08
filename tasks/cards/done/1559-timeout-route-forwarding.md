# Card 1559 — timeout-route-forwarding

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: middleware/timeout/timeout.go, middleware/httpmetrics/route_timeout_test.go, middleware/timeout/README.md, tasks/cards/done/1559-timeout-route-forwarding.md
Depends On: 1557-observation-route-receiver, 1558-router-route-publication

## Goal

Forward route metadata across buffered timeout boundaries safely.

## Scope

Only the owned files. Target module manifest read: middleware/module.yaml.

## Non-goals

No stable public API changes, new dependencies, or unrelated cleanup.

## Files

- middleware/timeout/timeout.go
- middleware/httpmetrics/route_timeout_test.go
- middleware/timeout/README.md
- tasks/cards/done/1559-timeout-route-forwarding.md

## Impact and Ownership

The preceding review reproduced this failure. Preserve cancellation, panic,
and resource ownership. Route metadata is immutable and synchronized; ordinary
responses freeze headers at commitment. No credentials enter diagnostics.

## Acceptance Tests

middleware/httpmetrics/route_timeout_test.go: TestOuterObservationsWithTimeout

## Tests

Cover the reproduced negative path, successful behavior, and applicable
concurrent interleavings using channels rather than timing synchronization.

## Docs Sync

middleware/timeout/README.md

## Validation

- `go test -race -timeout 60s ./middleware/...`
- `go run ./internal/checks/agent-workflow`
- `GOMAXPROCS=4 make gates`

## Done Definition

- [x] Acceptance Tests pass.
- [x] All Validation commands exit 0.
- [x] Documentation and formatting match behavior.

## Outcome

Forward route identity through timeout buffering without response access. Normal completion and publication after cancellation passed under race.

Validated on 2026-11-08: owning-module race tests, repository-wide ordinary
and race tests, boundary/API/docs/reference/CLI gates, lint (zero findings),
and website sync passed. Stable-module coverage is 95.8%. Full acceptance used
GOMAXPROCS=4 without changing assertions or the 60-second timeout. Default
parallelism twice reached the CLI commands package timeout; an independent
original-parameter retry passed in 33.5 seconds and a fresh bounded-parallelism
CLI run passed in 48 seconds. The CLI runtime sensitivity remains a follow-up,
not a behavior change in this card. Generated website files remain unchanged.
No stable public API or dependency changes. Changes remain uncommitted.

