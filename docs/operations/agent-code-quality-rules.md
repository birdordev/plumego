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
- [P0/P1/P2/P3] Title
  Checklist IDs:
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

## 9. Stable Module Quality Checklist

This checklist applies to `core`, `router`, `contract`, `middleware`,
`security`, `store`, `health`, `log`, `metrics`, and the root facade. Keep the
numbers stable so reviews, task cards, and implementation handoffs can refer
to the same checks. Apply the authority order in [AGENTS.md](../../AGENTS.md),
including its module boundaries, and follow the gate selection in [§6](#6-gate-profiles).

### How to apply the checklist

- **Review.** Start with the module manifests and identify the public entrypoints,
  internal paths, and representative callers inspected. For a full stable-root
  review, account for all 52 IDs and each of the nine modules. Record evidence
  for checked items, explain items that do not apply, and mark unverified items
  with the missing evidence. Passing gates alone establishes only what those
  gates checked.
- **Prioritize.** Use P0–P3 and the evidence statuses in
  [§8](#severity-and-evidence). Tie each confirmed finding to relevant checklist
  IDs and code locations. Record the trigger, actual and expected behavior,
  impact, smallest fix, and missing regression coverage using
  [§5](#5-review-output-contract). Keep unverified hypotheses identifiable.
- **Plan and implement.** In each task card, name the owning module, applicable
  IDs, files in scope, expected behavior, regression commands, and completion
  criteria. For a scoped change, apply the relevant cross-cutting checks and
  the owning module's check. Review authorization covers inspection and
  reporting. Implement after the task authorizes edits.
- **Verify and hand off.** Compare the outcome with the card, record the actual
  commands and results, and identify remaining evidence gaps. Use
  [§7](#7-handoff-contract) for implementation handoffs.

### Architecture and compatibility

1. **Establish module ownership.** Read the owning module's `module.yaml`
   before changing behavior. Place implementation, tests, and documentation
   with the module that owns the contract.
2. **Verify dependency direction.** Stable modules must not import `x/*`,
   reference applications, or CLI packages. Check cross-module imports against
   [the dependency rules](../../specs/dependency-rules.yaml).
3. **Keep the root facade thin.** Limit the root package to its approved
   convenience exports. Repository code imports the package that owns the capability.
4. **Prefer the standard library.** Obtain explicit approval for new third-party
   dependencies in the main module. Use existing `internal/*` helpers only
   from their declared callers.
5. **Make construction and dependencies visible.** Pass dependencies through
   constructors and fields. Follow [standard-service](../../reference/standard-service)
   for application wiring. Avoid mutable global registries, `init()`
   registration, reflection-based discovery, and context service locators.
6. **Preserve stable caller contracts.** Check signatures, defaults, error
   codes, return shapes, and documented behavior. For an authorized contract
   change, inventory and migrate callers and synchronize public entrypoints
   and API snapshots.
7. **Give compatibility layers an explicit disposition.** A retained alias or
   deprecated wrapper needs a replacement, an owner, and a migration decision.
   Apply the removal rules when its last caller migrates and record exceptions
   in [the deprecation inventory](../../specs/deprecation-inventory.yaml).
8. **Keep changes independently reviewable and reversible.** Default to one
   primary module. For cross-module work, identify each affected contract and
   its verification scope.

### Readability and maintainability

9. **Choose names that express intent.** Function and type names convey
   responsibilities. Variable names distinguish units, state, and meaning.
10. **Make the main sequence readable.** Organize validation, state changes,
    external calls, and returns in execution order. Use early returns where
    they keep the success path shallow.
11. **Split by responsibility and phase.** Separate independent responsibilities,
    resource phases, and error policies when they make one function hard to
    follow. Function length alone is no gate.
12. **Extract helpers that explain or consolidate logic.** A helper names a
    coherent operation, centralizes an invariant, or removes repetition that
    could diverge. Keep forwarding-only operations at the call site by default.
13. **Derive small interfaces from callers.** Identify the consumer, replacement
    need, or test boundary for a new interface. Keep its method set narrow.
14. **Centralize configuration semantics.** Define defaults, zero values,
    invalid values, and explicit disablement. Validate and normalize at the
    entrypoint where practical.
15. **Make construction failures actionable.** New fallible constructors
    return an error. Use the documented safe entrypoint for existing
    compatibility constructors.
16. **Document constraints.** GoDoc describes behavior callers depend on.
    Implementation comments explain ordering, locks, ownership, and
    compatibility constraints.

### HTTP, errors, and sensitive data

17. **Preserve `net/http` compatibility.** Handlers use
    `func(http.ResponseWriter, *http.Request)` and middleware wraps `http.Handler`.
18. **Make input sources visible.** Show whether a value comes from the path,
    query, headers, or body. Decode JSON explicitly or through a thin helper
    that reads a single source.
19. **Make route registration searchable.** Keep method, path, and handler
    visible together. Return registration failures to the assembly layer and
    handle them before starting the app.
20. **Keep middleware control flow deterministic.** Call `next` once on pass
    through. On rejection, complete the response and return. Keep business
    policy, DTO assembly, and service injection in their owning layers.
21. **Use the canonical JSON response paths.** Use `contract.WriteResponse`
    for success and `contract.WriteError` for failure. Preserve stable status
    codes, error codes, and response shapes.
22. **Keep errors classifiable.** Preserve `errors.Is` and `errors.As` where
    callers branch on errors, and add enough operation context. Update affected
    callers to handle newly returned errors.
23. **Separate failures by HTTP commitment.** Establish response ownership
    during encoding failures, post-commit write failures, and recovery.
    Avoid writing a second final status or error body.
24. **Verify applicable writer capabilities.** Exercise streaming, connection
    takeover, and `http.ResponseController` through actual wrappers where
    relevant. Cover informational headers, final headers, and bodyless responses.
25. **Protect sensitive data on every output path.** Inspect response bodies,
    logs, error chains, and fallback diagnostics. Keep tokens, signatures,
    private keys, and replayable derived values out of those paths.

### State, concurrency, and resources

26. **Propagate cancellation to blocking operations.** Pass caller contexts to
    I/O and downstream calls. Normalize nil only at entrypoints that accept it.
27. **Limit context to request-related data.** Use `With{Type}` and
    `{Type}FromContext` with unexported zero-value struct key types.
28. **Enumerate state transitions.** Specify initial, success, and failure
    states for start, stop, reset, and other stateful operations. Account for
    repeated calls and concurrency where applicable.
29. **Assign resource ownership.** Trace creation, exit conditions, and
    cleanup responsibility for files, connections, timers, and goroutines.
    Cover partial initialization.
30. **Preserve primary and relevant cleanup errors.** On rollback failure,
    identify what remains to clean up. Retry only the steps still pending.
31. **Define the commit point and retry semantics.** Distinguish pre-commit
    failures, failures after commitment, and cancellation racing with commit.
    State whether a retry resumes, repeats, or is rejected.
32. **Synchronize the whole invariant.** Cover related reads and writes with
    locks, atomics, or a single owner. Verify that fields that must change
    together cannot expose an inconsistent intermediate state.
33. **Define callback lock boundaries.** Invoke external callbacks outside locks
    by default. For callbacks under a lock, document and verify blocking,
    cancellation, and reentrancy constraints.
34. **Define ownership for mutable data.** State when maps, slices, labels, and
    snapshots are copied and who can mutate them. Test that caller mutations
    leave internal state isolated where the contract requires it.
35. **Bound resource growth.** Specify applicable limits, behavior at capacity,
    and exit conditions for request bodies, caches, buffers, queues, and workers.

### Stable module checks

36. **Verify `core` lifecycle.** Cover startup order, reverse rollback after
    partial startup, cleanup retry, controlled shutdown, and readiness changes.
37. **Verify `router` matching priority.** Cover static routes, parameters,
    groups, and reverse routing. Exercise dead-end fallback and complete-match
    priority, with equivalent cold and warm cache behavior for `HEAD`, 404,
    405, and `Allow`.
38. **Verify `contract` transport contracts.** Cover error codes, status
    codes, response fields, empty-value serialization, and encoding fallbacks
    through public entrypoints.
39. **Verify `middleware` composition order.** Make ordering visible in
    wiring and tests. Cover relevant pass-through, rejection, timeout, and
    recovery paths and their effects on the next handler and response.
40. **Verify `security` rejection paths.** Fail closed when credentials are
    invalid or expired, verification fails, or policy cannot be determined.
    Use timing-safe comparisons for secrets and signatures, with relevant
    negative tests.
41. **Verify `store` persistence consistency.** Cover supported historical
    data and failures before and after commitment. Check memory, disk,
    reopened state, and retries.
42. **Verify `health` status accuracy.** Distinguish liveness and readiness
    and handle unknown and failed checks by contract. Where the owning
    orchestrator manages results, verify that stale checks cannot overwrite
    newer state and that closing cannot restore readiness.
43. **Verify `log` output integrity.** Preserve field types and integer
    precision, escape text controls, bound cyclic and deep conversion, and
    verify fallback output.
44. **Verify `metrics` statistics consistency.** Assert exact totals after
    concurrent updates and invariants when reset and reads interleave. Isolate
    mutable fan-out labels and document aggregate snapshot atomicity.

### Tests and delivery

45. **Add focused behavior regressions.** Test observable outcomes using a
    normal or public entrypoint near the owning module. For defect fixes,
    prefer a regression that fails before the fix and passes afterward.
46. **Cover negative cases by risk.** Inject controlled failures for applicable
    invalid input, cancellation, partial success, dependency failures, and
    recovery. Assert the result and resulting state.
47. **Make concurrency tests deterministic.** Prefer channels, done signals,
    or repository condition-wait tools. Use fixed sleeps only for wall-clock
    behavior with no observable condition to wait on.
48. **Measure performance claims.** Supply reproducible before-and-after
    timings, allocations, or memory for relevant scenarios. Validate semantic
    equivalence and resource bounds when changing caches or pools.
49. **Check formatting and lint separately.** Format Go changes with `make fmt`.
    Run `make lint` as the zero-finding gate. Document any intentional
    exception at its code site.
50. **Select gates from the diff.** Preview with `make validate-diff-dry` and
    run `make validate-diff`. Follow [§6](#6-gate-profiles). A single stable
    module runs race and vet followed by boundary and API checks. Cross-module
    Go, public API, and release changes use `make gates`.
51. **Verify coverage and critical behavior.** The full gate requires at least
    90% aggregate stable-module statement coverage. Inspect affected failure,
    cancellation, rollback, and concurrency paths as well.
52. **Synchronize docs and record the delivery.** Update API snapshots,
    configuration, examples, and module docs when affected. Record actual
    validation commands, status, first meaningful failure, next step, and
    remaining verification scope.
