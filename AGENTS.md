# AGENTS.md - plumego

Operational guide for AI coding agents in `github.com/birdordev/plumego`. The root module declares Go 1.26.0+ and is stdlib-only unless an exception is explicitly approved.

## 1. Authority

Read path: `AGENTS.md` -> matching `specs/task-routing.yaml` entry -> that entry's `start_with` files -> target `<module>/module.yaml` before editing module behavior -> extra docs/specs only when preflight identifies a concrete need. Conflict order: (1) security and boundary rules here -> (2) `docs/reference/canonical-style-guide.md` -> (3) machine-readable specs -> (4) existing local patterns.

Control-plane docs: `docs/operations/codex-workflow.md`, `docs/operations/agent-code-quality-rules.md`, `docs/operations/agent-context-budget.md`, `docs/reference/canonical-style-guide.md`, `docs/concepts/agent-first-repo-blueprint.md`, and `docs/concepts/extension-maturity.md`.

Machine-readable sources: `specs/repo.yaml`, `specs/task-routing.yaml`, `specs/dependency-rules.yaml`, `specs/agent-quality-rules.yaml`, `specs/checks.yaml`, `specs/gate-profiles.yaml`, `specs/stop-condition-handlers.yaml`, `specs/request-flows.yaml`, `specs/extension-maturity.yaml`, `specs/extension-beta-evidence.yaml`, `specs/deprecation-inventory.yaml`, and `specs/change-recipes/`.

Canonical wiring: `reference/standard-service`. Agent entry shim: `CLAUDE.md` delegates to this file; edit `AGENTS.md` first when agent instructions drift. CLI: `(cd cmd/plumego && go run . --help)`; agent helpers live under `plumego agents ...`. Task bundle: `make bundle TASK=<recipe> MODULE=<path>`.

## 2. Non-Negotiables

- Preserve `net/http` compatibility: handlers remain `func(http.ResponseWriter, *http.Request)` and middleware wraps `http.Handler`.
- Main module code is stdlib-only unless approved; `x/*` is part of the main module and must not contain nested `go.mod` files.
- Stable roots must not import `x/*`, `reference/*`, `cmd/*`, or use-case modules.
- The root package `github.com/birdordev/plumego` is an approved thin facade only: `New`, `NewWithConfig`, `DefaultConfig`, `AppConfig`, `AppDependencies`, and `Param`. Do not expand it into custom handlers, context helpers, response helpers, or extension wiring, and do not import it from inside this repository.
- `reference/*`, `examples/*` and `cmd/plumego` may be standalone modules with their own `go.mod`. External dependencies there must stay isolated, use local `replace github.com/birdordev/plumego => ...` when developing against this checkout, and have a clear scenario/tooling rationale.
- No hidden globals, `init()` registration, route auto-discovery, reflection-based wiring, or context service-locator patterns.
- Never log or return secrets, tokens, signatures, private keys, or derived values that can replay credentials. Auth, verification, and policy errors fail closed; secret/signature comparisons use timing-safe checks.
- Context accessors use `With{Type}` + `{Type}FromContext`; context key types are unexported zero-value structs declared at the call site.
- `contract` owns transport primitives only: response/error helpers, request metadata, accessors, and binding. Keep one canonical success-response path and one canonical error path per layer.
- Remove deprecated symbols in the same change that removes their last caller. Do not leave dead wrappers or compatibility aliases without an explicit keep/remove decision in `specs/deprecation-inventory.yaml`.

## 3. Where To Work

Root facade: `plumego.go` is a convenience import for application callers only. Its primer is `docs/modules/plumego/README.md`; stable and extension code in this repo must import the owning packages directly.

Stable roots: `core router contract middleware security store health log metrics`.

Extensions under `x/`: beta families are `frontend gateway messaging observability rest tenant websocket`; experimental families are `ai data fileapi openapi resilience rpc validate`. Selected subpackages may have beta-surface status, currently tracked in `docs/concepts/extension-maturity.md` and `specs/extension-maturity.yaml`; do not infer parent-family promotion from a subpackage.

Landing zones: kernel/lifecycle/transport/auth/storage -> stable root; product capability/protocol/feature -> `x/*`; root convenience import -> `plumego.go` only; app wiring/DI/bootstrap -> `reference/standard-service`; CLI/scaffold/agent tooling -> `cmd/plumego` or `internal/tools`; workflow/specs/quality -> `docs/` or `specs/`; plans/sequencing -> `tasks/`.

New library code lives under an existing stable root or `x/*`. Do not add broad top-level library roots such as `ai`, `tenant`, `net`, `pubsub`, `rest`, `validator`, `utils`, or `frontend`.

Reference starts:

| Need | Start |
|---|---|
| JSON API / canonical layout | `reference/standard-service` |
| Hardened production baseline | `reference/production-service` |
| REST CRUD | `reference/with-rest` |
| Multi-tenant API | `reference/with-tenant` |
| Tenant administration | `reference/with-tenant-admin` |
| LLM/AI | `reference/with-ai` |
| WebSocket | `reference/with-websocket` |
| gRPC / RPC | `reference/with-rpc` |
| Observability | `reference/with-observability` |
| Protected ops/admin routes | `reference/with-ops` |
| Gateway/proxy | `reference/with-gateway` |
| Messaging | `reference/with-messaging` |
| Events / pubsub | `reference/with-events` |
| Webhook ingress/delivery | `reference/with-webhook` |
| Frontend/static assets | `reference/with-frontend` |
| Benchmark comparisons | `reference/benchmark` |

Boundaries: `middleware` is transport-only; tenant resolution, session lifecycle, quota, and tenant policy belong in `x/tenant`; broader tracing, metrics export, diagnostics, and adapter wiring belong in `x/observability`, not `contract`.

## 4. Working Contract

Defaults: one primary module, no stable public API change, no new dependencies, focused tests for behavior changes, docs sync only for implemented behavior/API/config/security/lifecycle/boundary changes.

Use analysis mode with no edits when ownership is unclear, acceptance criteria are missing, an unstated public API change is required, a new dependency is required, or a spec/manifest/pattern conflict changes behavior. Resolve stops via `specs/stop-condition-handlers.yaml`.

Preflight before editing, state each item: context package; owning module; target module manifest read; in-scope paths; out-of-scope paths; public API impact; dependency impact; behavior impact; security impact; docs impact; validation plan.

For module behavior, read the target `<module>/module.yaml` before editing. For control-plane/docs-only work with no target module, say `target module manifest: not applicable`.

## 5. Change Rules

- Keep changes minimal and scoped to the owning module or control-plane surface. Avoid opportunistic refactors.
- Preserve stable public APIs unless explicitly asked. For unavoidable breaking changes, use `specs/change-recipes/symbol-change.yaml`, enumerate callers with `rg -n --glob '*.go' 'SymbolName' .`, migrate all callers, re-search, update tests, and run `go build ./...` or the relevant full gate.
- Prefer stdlib and existing local patterns. Add or update tests next to changed behavior.
- No caller may silently discard a newly returned error.
- Public entrypoints and root-facade exports must stay in sync with `docs/evidence/stable-api/` and the `public-entrypoints-sync` check.
- For internal helper packages, use only declared caller paths from `specs/dependency-rules.yaml`; do not turn `internal/*` into shared utility buckets.

## 6. Validation

Prefer `make validate-diff` for the current diff; use `make validate-diff-dry` when you only need to see the selected profile. Validation order is module tests first, then boundary checks, then repo-wide checks when the gate profile requires them.

Lint is a zero-finding gate: `make lint` runs golangci-lint (v2 config in `.golangci.yml`, standard linter set). Fix findings in code rather than adding exclusion rules; an intentional exception needs a `//nolint` comment with a reason.

Baseline boundary/manifest checks are:

```bash
go run ./internal/checks/dependency-rules
go run ./internal/checks/cross-extension-deps
go run ./internal/checks/agent-workflow
go run ./internal/checks/module-manifests
go run ./internal/checks/reference-layout
go run ./internal/checks/extension-maturity
go run ./internal/checks/extension-beta-evidence
go run ./internal/checks/deprecation-inventory -strict
go run ./internal/checks/public-entrypoints-sync
```

`make gates` mirrors CI for the main repo: boundary checks, stable API snapshot comparison for `core`, doc snippet checks, `go vet ./...`, `make reference-vet`, gofmt cleanliness, race tests, `make reference-test`, stable coverage >= 90%, `cmd/plumego` vet/race tests, and `website/src/generated` staleness. Run `make website-gates` separately for the docs website static checks/build.

Docs-only changes may skip Go gates unless code, config, generated data, or runnable examples changed. Still validate accuracy, links, authority order, and any generated-doc staleness implied by the edited source.

Summarize validation as: command, status, key failure if any, next step.

## 7. Docs Sync

Update docs only for implemented behavior/API/config/security/lifecycle/boundary changes. Document what exists, not intended future behavior.

Sync targets when affected: `README.md`, `README_CN.md`, `AGENTS.md`, `CLAUDE.md` snapshot facts, `docs/operations/agent-context-budget.md`, `env.example`, affected `docs/modules/` primers, `docs/concepts/extension-maturity.md`, `docs/release/roadmap.md`, `docs/evidence/stable-api/`, and relevant `reference/*/README.md`.

Run `make website-sync` only when editing a source that feeds `website/src/generated/`, then commit the generated files together:

| Source | Generated |
|---|---|
| `docs/release/roadmap.md` | `roadmap.ts` |
| `docs/modules/*/README.md` | `modules.ts` |
| `specs/task-routing.yaml` | `routing.ts` |
| `README.md` + `specs/task-routing.yaml` | `releases.ts` |
| `website/src/content/docs/**/*.mdx` | `translation-lag.ts` |

`translation-lag.ts` derives from git timestamps; regenerate after every `.mdx` commit. Editing `AGENTS.md` alone does not require `make website-sync`.

## 8. Milestones

Milestones live at `tasks/milestones/active/M-NNN-short-name/M-NNN.md`. Read the listed context files first, stay inside affected modules, follow tasks in order, use the spec branch, record blockers, run full acceptance criteria, and package the PR via `docs/assets/github-workflows/milestone-pr-template.md`.

Task cards live at `tasks/cards/active/NNNN-slug.md` and should be narrow, reversible, and usually single-module. Move completed cards to `tasks/cards/done/`. Scaffold and validation helpers:

```bash
make new-milestone N=NNN TITLE="..."
make new-plan M=active/M-NNN-short-name
make new-card ID=0001 SLUG=slice-router-work M=M-001 R=fix-bug
make new-verify M=active/M-NNN-short-name
make check-card C=active/NNNN-slug
make run-card C=active/NNNN-slug
make milestone-status M=active/M-NNN-short-name
```

## 9. Anti-Patterns

No: new deps without approval; stable roots importing `x/*`; expanding the root facade; broad new top-level library roots; generic `utils` packages; route auto-discovery; reflection-based wiring; hidden mutable registries; `init()` registration; context service location; middleware that builds DTOs, injects business services, or branches on domain policy; ad hoc JSON response helpers; per-feature error envelopes; new handler signatures; new panic-only constructors for fallible behavior; compatibility wrappers without an explicit migration/keep decision.
