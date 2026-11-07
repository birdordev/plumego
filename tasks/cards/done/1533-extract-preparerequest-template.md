# Card 1533 — Extract PrepareRequest observer template

Milestone: M-027
Recipe: (none — pure refactor)
Context Package: middleware
Priority: P1
State: done
Primary Module: middleware/internal/telemetry
Owned Files: middleware/internal/telemetry/helpers.go, middleware/accesslog/accesslog.go, middleware/httpmetrics/http_metrics.go, middleware/tracing/tracing.go, middleware/requestid/request_id.go
Depends On: (none)

## Goal

Extract the duplicated `PrepareRequest → defer Complete` pattern from four observability middleware into a single `WrapWithObserver` helper, reducing duplication and ensuring consistent panic-preserving finalization.

## Problem

The same sequence is duplicated in four middleware:

1. `internaltelemetry.PrepareRequest(w, r)`
2. `r = prepared.Request`
3. `recorder := prepared.Recorder`
4. `defer internaltelemetry.FinishPreservingPanic(func() { prepared.Complete(r); ... })`

If a change is needed to the span header injection timing or panic preservation logic, all four files must be updated.

## Scope

- Extract `WrapWithObserver(next http.Handler, w, r *http.Request, observe ...)` into `middleware/internal/telemetry/pipeline.go`
- Migrate `accesslog`, `httpmetrics`, `tracing`, `requestid` to use the common helper
- Preserve exact behavior: request ID stamping, span header injection, panic preservation, metrics ordering
- Update tests to cover the unified helper

## Non-goals

- Do not change the `PreparedRequest` struct fields or `Complete()` signature
- Do not change `x/observability` adapters
- Do not introduce new public API (helper remains internal)

## Files

- middleware/internal/telemetry/pipeline.go (new)
- middleware/internal/telemetry/… (tests for WrapWithObserver)
- middleware/accesslog/accesslog.go
- middleware/httpmetrics/http_metrics.go
- middleware/tracing/tracing.go
- middleware/requestid/request_id.go

## Acceptance Criteria

- `WrapWithObserver` is defined in `internal/telemetry` and used by all four middleware
- Duplicate `FinishPreservingPanic` + `Complete` sequences removed
- Middleware tests pass without behavior change
- `make validate-diff` passes

## Validation

```bash
make validate-diff
go test ./middleware/... -count=1
```

## Done Definition

- [ ] `WrapWithObserver` implemented and tested in `internal/telemetry`
- [ ] All four middleware migrated and passing unit tests
- [ ] No duplicate `FinishPreservingPanic` + `Complete` sequences remain
- [ ] `go build ./...` clean
- [ ] `make validate-diff` clean

## Notes

`requestid` currently bypasses `PrepareRequest`; if it adds metrics/tracing support later, the unified helper makes the migration trivial.
