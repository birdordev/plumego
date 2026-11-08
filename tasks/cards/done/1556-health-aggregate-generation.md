# Card 1556 — health-aggregate-generation

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: x/observability/ops
Owned Files: x/observability/ops/healthhttp/manager.go, x/observability/ops/healthhttp/aggregate_regression_test.go, docs/modules/x/observability/ops/README.md, tasks/cards/done/1556-health-aggregate-generation.md
Depends On: none

## Goal

Prevent older aggregate checks from publishing over newer readiness.

## Scope

Only the owned files. Target module manifest read: x/observability/ops/module.yaml.

## Non-goals

No stable public API changes, new dependencies, or unrelated cleanup.

## Files

- x/observability/ops/healthhttp/manager.go
- x/observability/ops/healthhttp/aggregate_regression_test.go
- docs/modules/x/observability/ops/README.md
- tasks/cards/done/1556-health-aggregate-generation.md

## Impact and Ownership

The preceding review reproduced this failure. Preserve cancellation, panic,
and resource ownership. Route metadata is immutable and synchronized; ordinary
responses freeze headers at commitment. No credentials enter diagnostics.

## Acceptance Tests

x/observability/ops/healthhttp/aggregate_regression_test.go: TestAggregateGeneration

## Tests

Cover the reproduced negative path, successful behavior, and applicable
concurrent interleavings using channels rather than timing synchronization.

## Docs Sync

docs/modules/x/observability/ops/README.md

## Validation

- `go test -race -timeout 60s ./x/observability/ops/healthhttp`
- `go run ./internal/checks/agent-workflow`
- `GOMAXPROCS=4 make gates`

## Done Definition

- [x] Acceptance Tests pass.
- [x] All Validation commands exit 0.
- [x] Documentation and formatting match behavior.

## Outcome

Version aggregate batches independently of component checks; older batches cannot replace newer readiness. Channel-controlled newer-success and newer-failure regressions passed.

Validated on 2026-11-08: owning-module race tests, repository-wide ordinary
and race tests, boundary/API/docs/reference/CLI gates, lint (zero findings),
and website sync passed. Stable-module coverage is 95.8%. Full acceptance used
GOMAXPROCS=4 without changing assertions or the 60-second timeout. Default
parallelism twice reached the CLI commands package timeout; an independent
original-parameter retry passed in 33.5 seconds and a fresh bounded-parallelism
CLI run passed in 48 seconds. The CLI runtime sensitivity remains a follow-up,
not a behavior change in this card. Generated website files remain unchanged.
No stable public API or dependency changes. Changes remain uncommitted.

