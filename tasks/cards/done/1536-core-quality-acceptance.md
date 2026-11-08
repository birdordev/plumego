# Card 1536 — Implement core quality acceptance standard

Milestone: none
Recipe: none (control-plane quality standard, no runtime behavior change)
Context Package: control-plane
Priority: P2
State: done
Primary Module: control-plane
Owned Files: docs/operations/agent-code-quality-rules.md, specs/agent-quality-rules.yaml, specs/change-recipes/stable-root-boundary-review.yaml, tasks/cards/TEMPLATE.md, tasks/cards/done/1536-core-quality-acceptance.md
Depends On: none

## Goal

Make the agreed quality standard actionable through the existing human rules,
machine-readable focus, review recipe, and task-card template.

## Scope

Five files listed above. Target module manifest: not applicable.

## Non-goals

No API, dependency, runtime behavior, or module ownership changes. Existing
runtime regressions remain the baseline; do not claim a fresh full-module
audit from passing tests or introduce speculative runtime refactors.

## Files

- docs/operations/agent-code-quality-rules.md
- specs/agent-quality-rules.yaml
- specs/change-recipes/stable-root-boundary-review.yaml
- tasks/cards/TEMPLATE.md
- tasks/cards/done/1536-core-quality-acceptance.md

## Tests

Check that all nine stable modules have review focus; human and machine rules
agree on pass-through versus rejection, finding evidence, and severity. Confirm
reference paths exist and existing workflow checks accept the updates.

## Existing Regression Baseline

These are entrypoints to existing regression evidence, not a claim of a new
complete source audit. The full gate re-runs these tests.

| Owner | Evidence entrypoint | Focus |
|---|---|---|
| core | core/lifecycle_group_test.go | Partial startup, retained rollback failures, cleanup retry, readiness |
| router | router/router_contract_test.go | Cache invalidation, matching priority, HEAD, 405 |
| contract | contract/response_test.go; contract/errors_test.go | Encoding and write failures, canonical responses |
| middleware | middleware/stack_failure_regression_test.go | Cancellation, timeout and body-limit response ownership |
| security | security/jwt/security_regression_test.go | Invalid credentials, expiration, key consistency, secret exposure |
| store | store/kv/state_regression_test.go; store/db/transaction_regression_test.go; store/cache/state_regression_test.go | Commit failures, rollback, cancellation, concurrent updates |
| health orchestration | x/observability/ops/healthhttp/state_regression_test.go | Aggregation, stale checks, shutdown, detached snapshots |
| log | log/safety_regression_test.go | Precision, fallback leakage, cycles, text escaping |
| metrics | metrics/concurrency_regression_test.go | Label isolation, record/reset/snapshot concurrency |

## Docs Sync

Update existing quality authority in place; no new competing standard document.
No generated website source is affected.

## Validation

- `git diff --check`
- `make validate-diff`
- `make gates`

## Done Definition

- [x] Every acceptance dimension has observable evidence.
- [x] All nine stable module focus areas are represented.
- [x] Review recipe and card template capture state, ownership, and evidence.
- [x] Validation results and skipped checks are recorded accurately.

## Outcome

Completed on 2026-11-08. Updated existing standards without changing runtime
behavior, public APIs, dependencies, or module ownership.

Validation:
- `git diff --check`: passed.
- `make validate-diff`: passed, docs_only profile.
- `make gates`: passed, including full race, vet, API snapshots, reference
  checks, CLI checks, and generated website consistency. Stable coverage: 95.9%.
  The first sandboxed attempt could not write the external Go module cache;
  the authorized rerun completed with exit 0.

Skipped: standalone lint (golangci-lint unavailable), website build (no website
source changes). Residual risk: qualitative readability and ownership criteria
still require human/source review; passing gates is not a new full-module audit.
No runtime findings are asserted by this documentation change.
