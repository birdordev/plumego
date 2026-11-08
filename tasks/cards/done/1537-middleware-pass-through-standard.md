# Card 1537 — Clarify canonical middleware pass-through rule

Milestone: none
Recipe: none (canonical documentation clarification)
Context Package: control-plane
Priority: P2
State: done
Primary Module: control-plane
Owned Files: docs/reference/canonical-style-guide.md, tasks/cards/done/1537-middleware-pass-through-standard.md
Depends On: 1536

## Goal

Keep the canonical guide consistent with the quality standard: call downstream
once on pass-through and never after rejection.

## Scope

Canonical middleware wording only. Target module manifest: not applicable.
No public API, dependency, security policy, or runtime behavior changes.

## Non-goals

No middleware implementation changes or new response paths.

## Files

- docs/reference/canonical-style-guide.md
- tasks/cards/done/1537-middleware-pass-through-standard.md

## Tests

Read the canonical and quality rules together; existing pass-through and
failure-stack tests remain the behavior baseline.

## Docs Sync

Canonical guide only; no generated website source is affected.

## Validation

- `git diff --check`
- `make validate-diff`
- `make gates`

## Done Definition

- [x] Canonical and quality rules agree on pass-through and rejection.
- [x] Validation results are recorded.

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
