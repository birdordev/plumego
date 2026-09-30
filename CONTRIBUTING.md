# Contributing to Plumego

Thank you for your interest in contributing! This document covers everything
you need to get a change merged. The short version: keep changes scoped, run
`make validate-diff` before pushing, and let the boundary checks tell you
what else needs attention.

For the full operational rules (module boundaries, anti-patterns, task
routing), read [`AGENTS.md`](AGENTS.md) — it is the authoritative guide for
both humans and AI coding agents working in this repository.

## Prerequisites

- **Go 1.26+** (the version declared in `go.mod`).
- **Node.js 24 + pnpm** — only if you are touching the documentation website
  under `website/`.
- No other tooling is required: all checks are plain `go run` invocations
  driven by the `Makefile`.

## Repository Layout

| Path | What lives here | Dependency rule |
| --- | --- | --- |
| `core`, `router`, `contract`, `middleware`, `security`, `store`, `health`, `log`, `metrics` | Stable roots (GA) | **stdlib only**, never import `x/*` |
| `x/*` | Extensions (beta / experimental) | May use external deps, declared in each `module.yaml` |
| `reference/*` | Reference applications | Separate modules with their own `go.mod` and a `replace` directive |
| `cmd/plumego` | CLI and scaffolding | Separate module |
| `internal/checks` | Boundary/quality checkers | Repo-internal |
| `docs`, `specs`, `tasks` | Control plane (rules, manifests, milestones) | — |
| `website` | Documentation site (Astro) | Node toolchain |

## Making a Change

1. **Pick the owning module.** Every capability has exactly one home; see the
   "Where To Work" table in `AGENTS.md` and `specs/task-routing.yaml`. If
   ownership is unclear, open an issue before writing code.
2. **Read the module manifest.** Before editing module behavior, read the
   target `<module>/module.yaml`.
3. **Keep the diff scoped.** One primary module per change; no opportunistic
   refactors. Public API changes to stable roots require explicit discussion
   first — open an issue.
4. **Add tests next to the behavior you changed.** Handler tests use
   `httptest`; domain logic is tested independently of HTTP wiring.
5. **Sync docs only for what you implemented** — behavior, API, config,
   security, lifecycle, or boundary changes. See
   [Documentation Sync](#documentation-sync) below.

## Validation

Run checks in this order; stop at the first failure.

```bash
make fmt             # gofmt -w (run first)

# Preferred for a focused diff: selects the minimal gate profile
make validate-diff

# Full gate suite (mirrors CI): boundary checks, vet, race tests,
# reference-module tests, coverage >= 70% on stable roots
make gates

# Only when you touched website/ or sources that feed website/src/generated/
make website-gates
```

The boundary checks that `make gates` runs are also runnable individually:

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

### Stable API snapshots

The exported surface of each stable root is snapshot-tested under
`docs/evidence/stable-api/snapshots/`. If you intentionally add or remove an
exported symbol in a stable root, regenerate the snapshot, e.g.:

```bash
go run ./internal/checks/extension-api-snapshot \
  -module ./security/... \
  -out docs/evidence/stable-api/snapshots/security-head.snapshot
```

CI fails on snapshot drift, so an unexpected diff means you changed a public
API — make sure that was intentional and documented in the PR.

## Commit Style

Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
with a scope naming the affected module:

```
fix(security): make NewLimiter fail closed as documented
perf(log): build glog header without per-line fmt.Sprintf allocation
docs(x/ai,timeout,style): thread context into template load
test: remove boundary timing races from JWT and request-id tests
```

Keep the subject under ~72 characters; put the "why" in the body.

## Pull Requests

- Fill in [`.github/pull_request_template.md`](.github/pull_request_template.md);
  its scope/boundary and verification sections are not optional boilerplate —
  the boundary checkers and reviewers use them.
- CI runs the same gates as `make gates` plus fuzz smoke tests
  (`contract`, `security/jwt`, `security/password`, `router`, `x/websocket`),
  cross-platform build/vet checks (macOS and Windows, with the full test
  suite on macOS), CodeQL analysis, and the website build. A green local
  `make gates` is the strongest signal your PR will pass.
- Dependency updates for GitHub Actions, `cmd/plumego`, the reference
  modules with third-party requirements, and the website are automated via
  [Dependabot](.github/dependabot.yml).
- Small, single-purpose PRs are reviewed fastest. A PR that spans a stable
  root, an extension, and a reference app will usually be asked to split.

## Documentation Sync

Some files are generated from source-of-truth documents. When you edit a
source in the left column, regenerate and commit the right column together:

| Source | Regenerates |
| --- | --- |
| `docs/release/roadmap.md` | `website/src/generated/roadmap.ts` |
| `docs/modules/*/README.md` | `website/src/generated/modules.ts` |
| `specs/task-routing.yaml` | `website/src/generated/routing.ts` |
| `README.md` + `specs/task-routing.yaml` | `website/src/generated/releases.ts` |
| `website/src/content/docs/**/*.mdx` | `website/src/generated/translation-lag.ts` |

Regeneration command: `make website-sync`.

## Reporting Security Issues

Do **not** open a public issue for security vulnerabilities — see
[`SECURITY.md`](SECURITY.md) for the private reporting process.

## License

By contributing, you agree that your contributions are licensed under the
MIT License that covers this repository.
