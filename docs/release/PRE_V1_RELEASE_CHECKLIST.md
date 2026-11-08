# Pre-v1 Release Checklist

This checklist defines the evidence required before publishing a Plumego pre-v1
release candidate or using that release as evidence for extension maturity.

It does not claim that v1 exists. Release status must match git tags and
verifiable gate output.

## Release Candidate Inputs

- Target ref: a concrete git commit.
- Tag shape: a pre-v1 or release-candidate tag, for example `v0.1.0-rc.1`.
- Release notes: implemented behavior only.
- Stable root API snapshots: files under `docs/evidence/stable-api/snapshots/`.
- Extension evidence snapshots: files under `docs/evidence/extension/snapshots/`.
- Gate output: recorded command list and pass/fail result.

## Required Commands

Run from the repository root:

```bash
go run ./internal/checks/dependency-rules
go run ./internal/checks/agent-workflow
go run ./internal/checks/module-manifests
go run ./internal/checks/reference-layout
go run ./internal/checks/extension-maturity
go run ./internal/checks/extension-beta-evidence
go vet ./...
gofmt -l .
go test -race -timeout 60s ./...
go test -timeout 20s ./...
go test -coverprofile=/tmp/plumego-stable.cover ./core ./router ./middleware/... ./contract ./security/... ./store/...
cd cmd/plumego && go vet ./...
cd cmd/plumego && go test -race -timeout 60s ./...
cd cmd/plumego && go test -timeout 60s ./...
cd website && pnpm sync
cd website && pnpm check
cd website && pnpm build
```

The release candidate is blocked if any command fails or if `gofmt -l .`
prints files. It is also blocked if `pnpm sync` changes files that are not
committed before tagging.

## CLI Install Evidence

`cmd/plumego` is a nested Go module that uses a repository-local `replace`
directive during development. Before release notes or user docs advertise tagged
CLI installation, run this smoke test against the concrete tag:

```bash
GOBIN="$(mktemp -d)" go install github.com/birdordev/plumego/cmd/plumego@<tag>
"$GOBIN/plumego" version
```

If this fails, the release may still document source installation from a checked
out repository, but it must list tagged CLI install as a known blocker.

## CLI Stable Smoke

The `cmd/plumego` test suite must include and pass smoke coverage for:

- `plumego new` generating a canonical project from a source checkout.
- `go mod tidy` in that generated project.
- `plumego build`, `plumego test`, and `plumego check` against the generated project.
- JSON and YAML `version` command-result output.
- Text command help output.

## Evidence Rules

- Do not use `HEAD` as a substitute for release-history evidence.
- Do not clear `release_history_missing` until two concrete release refs are
  recorded for the candidate module.
- Do not clear `api_snapshot_missing` until snapshots are tied to those release
  refs.
- Do not clear `owner_signoff_missing` without module owner approval recorded
  in the evidence document.
- Do not change an extension `module.yaml` status to `beta` or `ga` in the same
  change that first creates incomplete evidence.

## Release Notes Shape

Use this minimal structure:

```markdown
# Plumego <tag>

## Status

- Main module: pre-v1
- Stable roots: stable-root candidates
- Extensions: experimental unless explicitly listed otherwise

## Included

- Implemented behavior and fixes only.

## Evidence

- Commit: <sha>
- Gates: <commands and result>
- Stable API snapshots: <paths>
- Extension snapshots: <paths>

## Known Blockers

- List unresolved release, beta, or compatibility blockers.
```
