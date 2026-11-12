# Stable Root API Baseline

This directory records the exported API surface for Plumego stable root packages.
Snapshots are updated alongside releases and are used to detect unintended API drift.

The snapshots are baseline evidence, not a standalone compatibility promise.
The compatibility promise is governed by `docs/reference/deprecation.md` and tied to release
tags and release notes.

## Stable Roots

| Root | Snapshot | Role |
| --- | --- | --- |
| `core` | `snapshots/core-head.snapshot` | App lifecycle, route attachment, middleware attachment, server assembly |
| `router` | `snapshots/router-head.snapshot` | Matching, params, groups, reverse routing |
| `contract` | `snapshots/contract-head.snapshot` | Response helpers, error model, context accessors, binding helpers |
| `middleware` | `snapshots/middleware-head.snapshot` | Transport-only HTTP middleware |
| `security` | `snapshots/security-head.snapshot` | Auth, headers, input safety, abuse guard primitives |
| `store` | `snapshots/store-head.snapshot` | Persistence primitives and base contracts |
| `health` | `snapshots/health-head.snapshot` | Health state and readiness models |
| `log` | `snapshots/log-head.snapshot` | Logging contracts and base implementations |
| `metrics` | `snapshots/metrics-head.snapshot` | Metrics contracts and base collectors |

## v1 Audit Status

- All stable root manifests are marked `status: ga`.
- `make api-check` requires a baseline file for each stable root and
  compares it with the current exported API. The gate checks the `core` and
  `router` root packages and includes subpackages for the other stable roots.
- Stable root production code has no `x/*` imports.
- Retained stable compatibility paths are recorded in
  `specs/deprecation-inventory.yaml`.

## Retained Compatibility Paths

These paths stay for v1 compatibility and are not cleanup targets unless a
future symbol-change card migrates every caller in the same change:

- `contract.APIError` remains exported, but new code should construct values
  through `contract.NewErrorBuilder`.

## Regeneration

Use the existing API snapshot tool. Example:

```bash
go run ./internal/checks/extension-api-snapshot -module ./core -out docs/evidence/stable-api/snapshots/core-head.snapshot
```

For the remaining stable roots, use `/...`:

```bash
go run ./internal/checks/extension-api-snapshot -module ./middleware/... -out docs/evidence/stable-api/snapshots/middleware-head.snapshot
```

## Freeze Rules

- Do not treat these files as permission to expand the stable surface.
- Signature changes, removals, and behavior changes in stable roots require
  explicit review against `docs/reference/deprecation.md`.
- Add behavior regression tests before changing high-risk paths in `core`,
  `router`, `contract`, `middleware`, or `security`.
- Keep stable roots free of `x/*` imports and new third-party dependencies.

## Required Checks

Before claiming stable-root freeze readiness:

```bash
make gates
make test
```
