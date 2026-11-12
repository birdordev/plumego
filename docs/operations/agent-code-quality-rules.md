# Agent Code Quality Rules

This document turns Plumego's code-quality expectations into an agent-facing
contract. Use it with `AGENTS.md`, `docs/operations/codex-workflow.md`,
`docs/operations/agent-context-budget.md`, `docs/reference/canonical-style-guide.md`, and
`specs/agent-quality-rules.yaml`.

## 1. Rule Levels

All `AGENTS.md §2` non-negotiables are MUST rules. Additional MUST rules:

- Behavior changes must include focused tests.
- HTTP JSON APIs must use `contract.WriteResponse` and `contract.WriteError`.

**SHOULD**: one primary module per change; standard-library shapes; tests next to behavior; grep-friendly wiring; smallest safe context package; module checks first, boundary second, gates last; compact validation summaries.

**MAY**: extract helper when local repetition is clear; skip Go gates for docs-only; compatibility layer only with explicit migration window; split broad work into analysis pass first.

## 2. Preflight

The preflight template is in `AGENTS.md §4`. Stop in analysis-only mode when ownership, dependency impact, stable public API impact, or cross-module boundaries are unclear. Use `docs/operations/agent-context-budget.md` to choose the context package.

## 3. Module Quality Focus

### `core`

- Keep lifecycle explicit: return registration errors, attach middleware visibly, keep shutdown controllable.
- Do not turn `core` into a feature catalog.

### `router`

- Cover static routes, params, groups, method matching, and reverse routing.
- Do not add response writing, auth policy, or business validation.
- Keep matching behavior grep-friendly.

### `contract`

Transport primitives only; preserve error-code and response-shape stability. Full accessor, error-construction, response-write, and deprecation rules: `docs/reference/canonical-style-guide.md §17`.

### `middleware`

- Call `next` exactly once when passing a request through; do not call it after rejecting a request.
- Test ordering and error paths.
- Keep behavior transport-only.
- Do not inject business services, build business DTOs, or branch on domain policy.
- Fallible constructors should return `(..., error)`.

### `security`

- Use timing-safe comparison for secrets, tokens, and signatures.
- Fail closed on invalid or unverifiable input.
- Add negative tests for invalid token and signature paths.
- Keep sensitive values out of logs and responses.
- Do not hide security state behind context service-location.

### `store`

- Protect concurrent reads and writes.
- Propagate context cancellation.
- Keep persistence behavior testable.
- Do not import `core`, `router`, `middleware`, `security`, or `x/*`.

### `health`

- Keep stable health models separate from orchestration in `x/observability/ops/healthhttp`.
- Aggregate unknown and failed checks conservatively; distinguish liveness from readiness.
- Older checks must not overwrite newer results; closing must not restore readiness.
- Return detached mutable snapshots and keep backend error text out of health responses.

### `log`

- Preserve structured field types and integer precision.
- Bound cyclic or deeply nested field conversion; escape text controls and delimiters.
- Encoding fallbacks and writer-error diagnostics must not expose hidden fields or original payloads.
- Document ownership of nested field values and concurrent writer behavior.
- Do not claim automatic credential redaction for arbitrary caller messages or fields.

### `metrics`

- Define synchronization for record, reset, and snapshot operations.
- Return detached snapshot maps; isolate mutable labels between fanout targets.
- Test exact totals after concurrent updates and invariants during reset/read interleavings.
- Document whether multi-collector snapshots are globally atomic or independently sampled.

### `x/tenant`

- Preserve tenant isolation.
- Cover quota, policy, and session lifecycle with negative tests.
- Keep tenant behavior out of stable `middleware` and `store`.

## 4. Anti-Patterns

The canonical anti-patterns list lives in `AGENTS.md §9`. This section adds
review-facing context for why each pattern matters; it does not duplicate the
authoritative list.

| Anti-pattern | Why it matters |
|---|---|
| Stable `x/*` imports | Breaks the boundary the dependency checker enforces; causes stability regression cascade |
| `init()` registration or hidden globals | Eliminates grep-ability; forces load-order awareness across the codebase |
| Context service-locator | Prevents per-request dependency substitution; hides test seams |
| Business DTOs in middleware | Couples transport and domain; forces middleware to change when schema changes |
| Per-feature error envelopes | Breaks the single canonical `contract.WriteError` path; clients cannot rely on one shape |
| Panic-only constructors | Forces callers to recover or crash; prefer `New(...) (*T, error)` |
| Compatibility wrappers without removal plan | Dead code that agents must read and reason about indefinitely |

### Test synchronization

`time.Sleep` must not be used to synchronize with asynchronous state in
new tests (background goroutines, timers, cleanup loops). Sleep-based
synchronization both slows the suite and flakes under CI load. Instead:

- Poll a condition with `internal/testutil/wait` (`ForCondition`,
  `ForValue`) — it returns as soon as the condition holds and fails with a
  descriptive message on timeout.
- Prefer direct synchronization (channels, `sync.WaitGroup`, done signals)
  when the code under test exposes one.
- Treat timeout values as failure budgets, not expected durations: pick
  5–10x the worst case, since polling returns immediately once satisfied.

`time.Sleep` remains acceptable only when the test's subject is wall-clock
behavior itself (e.g. asserting a rate limiter's window or a token's expiry)
and no observable condition exists to poll.

Legacy sleep-based tests are migrated opportunistically when a test file is
touched for other reasons; do not expand their use.

## 5. Review Output Contract

Use findings-first output for reviews:

```text
Findings:
- [P0/P1/P2] Title
  File:
  Trigger:
  Observed behavior:
  Expected behavior:
  Evidence status: reproduced / source-confirmed / unverified
  Problem:
  Impact:
  Minimal fix:
  Missing test:

Boundary status:
Validation status:
Docs sync:
Residual risk:
```

Review priorities:

1. Boundary violations
2. Stable-root imports of `x/*`
3. `net/http` compatibility risk
4. Hidden globals or context service-location
5. Fail-open security behavior
6. Response or error path drift
7. Missing behavior tests
8. Docs, config, or example drift

## 6. Gate Profiles

`specs/gate-profiles.yaml` is the command and selection authority for both
`validate-diff` and task bundles. `specs/checks.yaml` indexes validation owners;
it does not duplicate commands. The Makefile owns reusable repository checks; `make check` is the shared source and contract composition.

| Profile | Scope |
|---|---|
| `website_only` | Website checks and build; mixed Go/website diffs retain both gate families |
| `docs_only` | Markdown accuracy, links, terminology, and authority order |
| `single_module_behavior` | Owning module race tests and vet |
| `stable_root_change` | Race tests and vet for the affected stable root, followed by `make boundary-check api-check` |
| `cross_module_public_api_release` | `make gates`, including stable coverage and CLI validation |

Use `make validate-diff-dry` to inspect selected commands. Format intentional
source changes before validation; checks must not rewrite source files.
Docs-only exemptions do not apply to executable tooling, configuration, or
runnable examples. Lint and website build checks remain explicit additions
(`make lint`, `make website-gates`); `make gates` is the full local Go gate.

## 7. Handoff Contract

Every implementation handoff should include:

```text
Context package:
Owning module:
Files changed:
Boundary impact:
API/dependency impact:
Tests added or updated:
Validation commands run:
Docs sync impact:
Residual risk:
```


## 8. Core Quality Acceptance Standard

Use these criteria together with the module-specific focus above. They do not
replace `AGENTS.md` or authorize public API changes. Distinguish a required
correctness fix from an optional readability improvement.

| Dimension | Acceptance evidence |
|---|---|
| Ownership | Manifest and imports agree; construction and dependencies are visible; stable packages do not acquire extension responsibilities. |
| Public contract | Defaults, zero values, invalid input, repeated calls, and concurrency rules are documented where relevant and tested at public entrypoints. |
| State and resources | Allowed transitions, resource owner, partial success, rollback failure, and retry behavior are explicit and observable. |
| Errors | Callers can classify relevant errors; primary and cleanup failures are preserved where needed; the resulting state is explainable. |
| Concurrency | Shared state has a synchronization policy; lock ordering and callback boundaries are clear; mutable inputs and outputs have documented owners. |
| Cancellation | Blocking work receives the caller context; tests distinguish cancellation before work, during work, and after irreversible commit. |
| Readability | Names express intent; the main path and side effects are locally understandable; comments explain constraints and reasons. |
| Abstraction | Interfaces serve actual callers; helpers represent a coherent concept; no speculative wrappers, hidden registries, or unowned compatibility layers. |
| Security | Unverifiable credentials fail closed; responses, logs, fallback diagnostics, and error chains do not expose credentials. |
| Capacity and performance | Cache hits preserve semantics; bounded resources and background work have capacity and exit policies; optimization claims have measurements. |
| Tests and docs | Tests assert observable behavior and invariants; docs describe implementation; changed contracts include regression evidence. |

### State and ownership review

For lifecycle or persistence changes, record a small transition table in the
card or test describing the starting state, event, success state, failure
state, retained resources, and legal retry. A returned error must not be
assumed to mean that nothing committed.

For shared maps, slices, labels, and snapshots, state whether they are copied,
immutable, or caller-owned. Do not hold a lock across an external callback
without an explicit blocking, cancellation, and reentrancy policy. A deadline
on a context does not by itself make mutex acquisition cancellable.

For HTTP changes, test response ownership after final headers are committed,
informational headers, write failures, and applicable `ResponseWriter`
capabilities. Router changes must cover dead-end fallback, complete-match
priority, cold/hot cache equivalence, `HEAD`, 404, 405, and `Allow`.

### Readability decisions

Split a function when it mixes independent responsibilities, resource phases,
or error policies. Do not use line count alone as a quality gate. Extract a
helper only when its name conveys a stable concept or removes meaningful
repetition; keep one obvious canonical path. Keep code, identifiers, and code
comments in English.

### Severity and evidence

- P0: exploitable authentication bypass, serious credential exposure, or widespread irreversible data corruption.
- P1: demonstrated correctness failures such as deadlocks, resource leaks, lost cleanup state, incorrect routing, or inconsistent commit semantics.
- P2: localized reliability defects, ambiguous contracts, demonstrated maintenance obstacles, or missing critical regression coverage.
- P3: naming, comments, and local structure improvements without a current correctness impact.

Record trigger, observed and expected behavior, impact, file location, minimal
fix, and missing test for each finding. Label source-confirmed conclusions
and unverified hypotheses separately from reproduced failures. An unverified
hypothesis is not a confirmed defect.

### Completion

- A behavior fix has focused regression evidence, preferably failing before the fix and passing afterward.
- A structural cleanup preserves public behavior and tests the affected contract when risk warrants it.
- API snapshots, examples, configuration, and module docs are synchronized when affected.
- Use `make validate-diff-dry` to inspect gate selection and `make validate-diff` to execute it; escalate to `make gates` for cross-module or public-contract work.
- `make lint` is a separate zero-finding gate. `make gates` does not imply lint or website build validation; use `make website-gates` when affected.
- Keep the stable coverage gate at >=90%; additionally inspect failure, cancellation, and concurrency paths. A percentage alone is not acceptance evidence.
- Report actual commands, status, first meaningful failure, next step, skipped checks, and residual risk. Do not mark an entire module audited merely because its tests pass.
