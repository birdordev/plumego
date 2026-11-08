# Card 1564 — logger-output-encoding-contract

Milestone: none
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: log
Owned Files: log/logger.go, docs/modules/log/README.md, tasks/cards/done/1564-logger-output-encoding-contract.md
Depends On: none

## Goal

Document safe output encoding for custom logger implementations.

## Scope

Only owned files. Target module manifest read: log/module.yaml.

## Non-goals

No new dependencies, API signatures, scanner suppressions, or unrelated changes.

## Files

- log/logger.go
- docs/modules/log/README.md
- tasks/cards/done/1564-logger-output-encoding-contract.md

## Impact and Ownership

Callers exclude sensitive error text; output backends encode original values.
No transport response or verification policy changes.

## Acceptance Tests

x/messaging/webhook/inbound_logging_test.go: TestInboundPublishFailureLogsSafely

## Tests

Signed GitHub and Stripe requests with a failing publisher, text/JSON output,
sensitive error canaries, and event IDs containing a newline.

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

Signed provider failure regressions pass for text and JSON output. Raw publisher error text is excluded; safe type and event metadata remain. Custom logger encoding responsibilities are explicit. Owning-module race tests, repository tests/vet, lint, website sync, and automatic diff validation passed. No public signature or dependency changes.

Validation on 2026-11-08 used GOMAXPROCS=4 and GOTOOLCHAIN=local. The first automatic diff run hit its 120-second command limit during repository race tests; an unchanged-parameter retry passed, with race tests completing in 46.638 seconds. No assertions, timeouts, or scanner rules were weakened. CodeQL was not available locally; GitHub alert closure requires a fresh scan after publication.
