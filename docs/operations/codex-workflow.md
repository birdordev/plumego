# Agent Workflow — Plumego

This document defines the default working loop for agents in `plumego`.

Use it with:

- `AGENTS.md` for hard rules and validation order
- `docs/reference/canonical-style-guide.md` for code shape
- `docs/operations/agent-code-quality-rules.md` for preflight, review, and gate selection
- `docs/operations/agent-context-budget.md` for bounded reads and resume discipline
- `specs/agent-quality-rules.yaml` for the machine-readable quality contract
- `specs/*` and `<module>/module.yaml` for routing, ownership, and validation

Repo-native change recipes live under `specs/change-recipes/`.

## 1. Working Modes

**Analysis** — when scope, ownership, or architecture is unclear. Return: owning module, in/out-of-scope paths, likely touched files, main risks, validation plan. Do not edit code.

**Implementation** — when the task contract is clear. Confirm owning module, make smallest coherent change within one module, add focused tests, run validation before handoff.

**Review** — when asked for review, audit, or risk assessment. Output: findings-first, severity-ordered, with file references and regression risks. Do not patch unless explicitly asked.

## 2. Default Task Contract

Default assumptions are in `AGENTS.md §4`. Every task should still be framed in terms of:

- Goal
- In Scope
- Out of Scope
- Target Module
- API and Dependency Policy
- Behavior, Security, and Docs Impact
- Tests
- Validation
- Done Definition

## 3. Default Loop

Follow the read path in `AGENTS.md §1`, then:

4. Identify the owning module or family.
5. Read the target `<module>/module.yaml` before editing module behavior.
6. State the context package, scope, and impact assumptions.
7. Implement the smallest coherent change.
8. Add or update focused tests.
9. Run module validation first.
10. Run boundary and repo-wide checks only when the gate profile requires them.
11. Report validation as a compact command and status summary with residual risk.

### Command entry points

Run `make help` for commands grouped by development, quality gates, website,
task artifacts, and local setup. The root Makefile handles orchestration;
`internal/tools/task-workflow` shares milestone resolution and template editing
for the task commands. Run `python3 internal/tools/task-workflow-test.py` after
changing those command paths; it uses temporary artifacts and stubbed tools.

Use three composition entry points:

- `make check`: formatting, tooling, boundaries, canonical assets, API snapshots,
  and documentation snippets.
- `make gates`: the root and reference suite (`make go-gates`) plus CLI vet and race tests (`make cli-check`).
- `make website-gates`: generated-data validation, website checks, and build.

Compositions stop at the first failure and preserve ordering under `make -j`.
`make lint` remains a separate zero-finding check. Individual checks such as
`make api-check` and `make stable-coverage` remain available for targeted work.

`specs/gate-profiles.yaml` owns diff selection and bundle validation commands.
`specs/checks.yaml` is an index, and `specs/agent-quality-rules.yaml` owns review
policy. Cross-module and control-plane diffs call `make gates`. The docs-only
profile applies to Markdown-only diffs. Website changes select website gates;
mixed Go/website diffs retain both gate families.

The pre-push hook uses `make pre-push-check` for every branch: formatting, vet,
snippets, and standard tests. Full validation belongs to CI or an explicit local
`make gates` run. Go checks do not require Node/pnpm; website checks do.
CI invokes `make go-gates` and `make cli-check` in separate jobs, and also runs
fuzz, lint, cross-platform, website, and CodeQL checks.

The single coverage gate measures stable modules at >=90%. CI saves that same
profile with `make go-gates COVERAGE_FILE=<path>` and uploads it as the
`coverage-report` artifact and to Codecov. There is no extra all-package coverage
run, HTML report, or PR coverage comment. A failing threshold does not export
a new profile; successful runs without `COVERAGE_FILE` leave no output artifact.

`make website-sync` regenerates website data without staging it. Review the diff
and explicitly stage the generated files with the corresponding source changes.
`make website-check-generated` detects changes introduced by regeneration and
propagates generation failures; temporary check files are removed on exit.

Use targeted `rg` searches when a symbol, package, or rule needs confirmation.
Avoid broad file dumps after ownership and validation are already clear.

## 4. Context Budget

Use the smallest context package that can safely complete the task.

- `startup`: initial orientation
- `implementation`: normal code or docs changes
- `review`: findings-first review
- `control-plane`: workflow, architecture, quality, or spec changes

See `docs/operations/agent-context-budget.md` for package contents, split thresholds, and
resume discipline.

Split work before implementation when the expected scope spans more than one
primary module, more than five files, more than three validation commands, or
unclear API, dependency, security, or boundary impact.

## 5. Stop Conditions

Stop conditions are in `AGENTS.md §4`. Use `specs/stop-condition-handlers.yaml` for the deterministic resolution path.

## 6. Recipes

Before writing a bespoke workflow, check whether one of these recipes already
matches the task:

- `specs/change-recipes/analysis-only.yaml`
- `specs/change-recipes/fix-bug.yaml`
- `specs/change-recipes/http-endpoint-bugfix.yaml`
- `specs/change-recipes/review-only.yaml`
- `specs/change-recipes/add-http-endpoint.yaml`
- `specs/change-recipes/add-middleware.yaml`
- `specs/change-recipes/add-acceptance-tests.yaml`
- `specs/change-recipes/new-stable-module.yaml`
- `specs/change-recipes/new-extension-module.yaml`
- `specs/change-recipes/stable-root-boundary-review.yaml`
- `specs/change-recipes/symbol-change.yaml`
- `specs/change-recipes/tenant-policy-change.yaml`
- `specs/change-recipes/add-websocket-room.yaml`
- `specs/change-recipes/add-ai-tool.yaml`
- `specs/change-recipes/add-grpc-method.yaml`

## 7. Prompt Shapes

### Analysis

```text
Do not edit. For [task]: return context package, owning module, in/out-of-scope paths,
likely files, risks, validation plan, smallest reversible task split.
```

### Implementation

```text
Implement in plumego: [task]
In scope: [paths] | Out of scope: [paths] | Target module: [module]
Follow AGENTS.md: complete preflight, state files before editing, smallest coherent
change, add focused tests, run validation, report residual risks.
```

### Review

```text
Review only, no code changes. Priorities: boundary violations → x/* stable-root imports
→ net/http risk → hidden globals/service-location → fail-open → response/error drift
→ missing tests → docs/config drift. Findings-first, severity-ordered, file refs.
Output contract: docs/operations/agent-code-quality-rules.md §5.
```
