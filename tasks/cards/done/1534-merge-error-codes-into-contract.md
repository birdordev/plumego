# Card 1534 — Merge transport error codes into contract

Milestone: M-027
Recipe: specs/change-recipes/symbol-change.yaml
Context Package: middleware
Priority: P1
State: done
Primary Module: contract, internal/httputil
Owned Files: contract/error_codes.go, internal/httputil/errors.go, middleware/concurrencylimit/concurrency_limit.go, middleware/singleflight/singleflight.go
Depends On: (none)

## Goal

Move the three middleware-specific error codes from `internal/httputil/errors.go` into `contract/error_codes.go`, eliminating a second source of error code truth and preparing `internal/httputil` to remain purely helper-oriented.

## Problem

Error code constants are maintained in two places:

- `contract/error_codes.go`: canonical domain codes (`CodeBadRequest`, `CodeRateLimited`, …)
- `internal/httputil/errors.go`: middleware-only codes (`CodeServerBusy`, `CodeServerQueueTimeout`, `CodeUpstreamFailed`)

This splits the error code surface and risks inconsistency when new codes are added.

## Scope

- Add `CodeServerBusy`, `CodeServerQueueTimeout`, `CodeUpstreamFailed` to `contract/error_codes.go`
- Update `internal/httputil/errors.go` to import and re-export from `contract` (deprecation path) or remove re-export
- Migrate callers in `middleware/concurrencylimit` and `middleware/singleflight` to use `contract.*`
- Follow `specs/change-recipes/symbol-change.yaml`: enumerate callers, migrate all, re-search, update tests

## Non-goals

- Do not change the string values of the codes (backward compatibility)
- Do not change `WriteTransportError` signature
- Do not introduce new error types or behavior changes

## Risk

| Risk | Mitigation |
|---|---|
| External importers reference `httputil.CodeServerBusy` | Add deprecated aliases in `httputil` for one minor version |
| `go build ./...` breaks in CI | Stage: contract update → httputil alias → caller migration |

## Files

- contract/error_codes.go
- internal/httputil/errors.go
- middleware/concurrencylimit/concurrency_limit.go
- middleware/singleflight/singleflight.go
- specs/deprecation-inventory.yaml (if aliases added)

## Acceptance Criteria

- `CodeServerBusy`, `CodeServerQueueTimeout`, `CodeUpstreamFailed` exist only in `contract`
- All internal middlewar callers use `contract.*`
- `internal/httputil/errors.go` contains no code definitions (only `WriteTransportError`)
- Build and tests pass

## Validation

```bash
go build ./...
go test ./middleware/concurrencylimit/... ./middleware/singleflight/... -count=1
make validate-diff
```

## Done Definition

- [ ] Error codes moved to `contract`
- [ ] All internal callers migrated
- [ ] `internal/httputil/errors.go` code-free
- [ ] `go build ./...` and `go test ./...` clean
- [ ] `specs/deprecation-inventory.yaml` updated if aliases added
