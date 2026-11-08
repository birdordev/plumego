# Card 1568 — text-depth-node-budget

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: log/logger.go, log/depth_budget_test.go, docs/modules/log/README.md, tasks/cards/done/1568-text-depth-node-budget.md
Depends On: none

## Goal

Consume node budgets before depth truncation.

## Scope

Only owned files. Target module manifest read: log/module.yaml.

## Non-goals

No stable public API or dependency changes; no unrelated cleanup.

## Files

- log/logger.go
- log/depth_budget_test.go
- docs/modules/log/README.md
- tasks/cards/done/1568-text-depth-node-budget.md

## Impact and Ownership

Transport preserves net/http semantics. Observation counts do not retain payloads.
Manual health updates invalidate older checks. Logging traversal stays bounded.

## Acceptance Tests

log/depth_budget_test.go

## Tests

Cover the reproduced negative paths and retain existing successful behavior.

## Docs Sync

docs/modules/log/README.md

## Validation

- `go test -race -timeout 60s ./log/...`
- `make validate-diff`

## Done Definition

- [x] Acceptance Tests pass.
- [x] All Validation commands exit 0.
- [x] Documentation and formatting match behavior.

## Outcome

Regression tests reproduced the original failures before implementation. Observation now preserves informational/final commitment, implicit headers, Flush and trailers using the underlying Header map. Counting uses a separate non-capturing internal constructor; gateway capture callers retain body capture. Text traversal consumes budget at the depth boundary. Manual readiness updates invalidate older aggregates and cannot change closed state; newer aggregates remain permitted.

Validated on 2026-11-08: affected-package and gateway race tests, cross-module automatic diff validation (repository race/ordinary tests, vet, boundary and manifest checks), lint with zero findings, and website sync passed. Final multi-layer status/byte tests and closed-manager regressions passed. No stable public API signatures or dependencies changed. Changes remain uncommitted.
