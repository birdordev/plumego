# Card 1567 — observer-count-without-capture

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: internal/httputil/http_response.go, middleware/internal/telemetry/helpers.go, middleware/httpmetrics/capture_regression_test.go, docs/modules/middleware/README.md, tasks/cards/done/1567-observer-count-without-capture.md
Depends On: none

## Goal

Count observation bytes without retaining response bodies.

## Scope

Only owned files. Target module manifest read: middleware/module.yaml.

## Non-goals

No stable public API or dependency changes; no unrelated cleanup.

## Files

- internal/httputil/http_response.go
- middleware/internal/telemetry/helpers.go
- middleware/httpmetrics/capture_regression_test.go
- docs/modules/middleware/README.md
- tasks/cards/done/1567-observer-count-without-capture.md

## Impact and Ownership

Transport preserves net/http semantics. Observation counts do not retain payloads.
Manual health updates invalidate older checks. Logging traversal stays bounded.

## Acceptance Tests

middleware/httpmetrics/capture_regression_test.go

## Tests

Cover the reproduced negative paths and retain existing successful behavior.

## Docs Sync

docs/modules/middleware/README.md

## Validation

- `go test -race -timeout 60s ./middleware/...`
- `make validate-diff`

## Done Definition

- [x] Acceptance Tests pass.
- [x] All Validation commands exit 0.
- [x] Documentation and formatting match behavior.

## Outcome

Regression tests reproduced the original failures before implementation. Observation now preserves informational/final commitment, implicit headers, Flush and trailers using the underlying Header map. Counting uses a separate non-capturing internal constructor; gateway capture callers retain body capture. Text traversal consumes budget at the depth boundary. Manual readiness updates invalidate older aggregates and cannot change closed state; newer aggregates remain permitted.

Validated on 2026-11-08: affected-package and gateway race tests, cross-module automatic diff validation (repository race/ordinary tests, vet, boundary and manifest checks), lint with zero findings, and website sync passed. Final multi-layer status/byte tests and closed-manager regressions passed. No stable public API signatures or dependencies changed. Changes remain uncommitted.
