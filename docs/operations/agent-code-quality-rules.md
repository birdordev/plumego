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

- Call `next` exactly once.
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

### docs-only

Check accuracy, links, terminology, and authority order. Go gates are not
required unless code, config, generated data, or runnable examples changed.

### single module behavior

```bash
go test -race -timeout 60s ./<module>/...
go test -timeout 20s ./<module>/...
go vet ./<module>/...
gofmt -w <changed-go-files>
```

### stable root change

```bash
go test -race -timeout 60s ./<module>/...
go test -timeout 20s ./<module>/...
go vet ./<module>/...
go run ./internal/checks/dependency-rules
go run ./internal/checks/agent-workflow
go run ./internal/checks/module-manifests
```

### router, middleware, or security

```bash
go test -race -timeout 60s ./<module>/...
go test -timeout 20s ./<module>/...
go vet ./<module>/...
go run ./internal/checks/dependency-rules
```

Also confirm:

- `router`: static, param, group, method, and reverse-route tests
- `middleware`: ordering, error-path, panic, timeout, and cancel tests
- `security`: invalid-token, invalid-signature, fail-closed, and timing-safe tests

### cross-module, public API, or release-relevant

```bash
go run ./internal/checks/dependency-rules
go run ./internal/checks/agent-workflow
go run ./internal/checks/module-manifests
go run ./internal/checks/reference-layout
go run ./internal/checks/extension-maturity
go run ./internal/checks/extension-beta-evidence
go run ./internal/checks/deprecation-inventory -strict
go test -race -timeout 60s ./...
go test -timeout 20s ./...
go vet ./...
gofmt -w .
```

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
