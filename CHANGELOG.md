# Changelog

All notable changes to Plumego are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html) for the
stable roots (`core`, `router`, `contract`, `middleware`, `security`,
`store`, `health`, `log`, `metrics`).

Extension families under `x/` carry their own maturity labels and are **not**
covered by the semver guarantee until they reach GA — see
[`docs/concepts/extension-maturity.md`](docs/concepts/extension-maturity.md).

Detailed, evidence-backed release documents live in
[`docs/release/`](docs/release/); this file is the human-readable index.

## [Unreleased]

## [1.1.0] - 2026-05-18

Full release notes: [`docs/release/v1.1.0.md`](docs/release/v1.1.0.md).

### Added

- Agent workflow improvements: expanded task routing, diff-validation,
  task-bundle, recipe, and manifest guidance so coding agents find landing
  zones and validation paths more deterministically.
- First `v1.0.0` release-ref evidence points and baseline API snapshots for
  selected experimental extension candidates (`x/tenant`, `x/ai/provider`,
  `x/ai/session`, `x/ai/streaming`, `x/ai/tool`, `x/data:file`,
  `x/data:idempotency`, `x/gateway/discovery:core-static`,
  `x/messaging:app-facing-service`).

### Changed

- Website and bilingual documentation synchronized and rebuilt under the
  website gate (release posture, status, architecture, migration, benchmark
  pages).
- Task-queue lifecycle rules tightened so active work remains executable and
  archived cards retain validation outcomes.
- GitHub Actions runtime and CLI install guidance aligned with the
  source-checkout validation model for the nested `cmd/plumego` module.

### Stable API

Zero exported-symbol additions, removals, or signature changes across all
nine stable roots versus `v1.0.0` (snapshot comparison evidence in the
release notes). Upgrading from `v1.0.0` requires no code changes.

## [1.0.0] - 2026-05-15

First stable release. Release decision record:
[`docs/release/v1.0.0.md`](docs/release/v1.0.0.md); full notes:
[`docs/release/V1-RELEASE-NOTES.md`](docs/release/V1-RELEASE-NOTES.md).

### Added

- Nine stable roots with a frozen public API and snapshot-tested surface:
  `core`, `router`, `contract`, `middleware`, `security`, `store`, `health`,
  `log`, `metrics` — all standard-library only.
- Fourteen extension families under `x/` with explicit beta/experimental
  maturity labels and per-module evidence tracking.
- Sixteen reference applications under `reference/` demonstrating the
  canonical wiring patterns, each a standalone module.
- The `plumego` CLI (`cmd/plumego`) for scaffolding and agent helpers.
- Machine-readable control plane under `specs/` with nine boundary checkers
  (`internal/checks/`) enforced in CI.

## [1.0.0-rc.1] - 2026-05-15

Release candidate for `v1.0.0`; observation window passed with no blocking
issues. Notes: [`docs/release/v1.0.0-rc.1.md`](docs/release/v1.0.0-rc.1.md).

## [0.2.0] - 2026-05-12

Pre-1.0 development snapshot. Not covered by the stability guarantee.

[Unreleased]: https://github.com/spcent/plumego/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/spcent/plumego/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/spcent/plumego/compare/v1.0.0-rc.1...v1.0.0
[1.0.0-rc.1]: https://github.com/spcent/plumego/compare/v0.2.0...v1.0.0-rc.1
[0.2.0]: https://github.com/spcent/plumego/releases/tag/v0.2.0
