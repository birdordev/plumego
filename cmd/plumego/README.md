# Plumego CLI

A code agent-friendly command-line tool for plumego projects. Designed for automation, CI/CD integration, and AI-assisted development workflows.

## Current Status

- Included in the Plumego planned v1 hardening scope
- Supported as a command-line tool, not as a Go library import surface
- Command behavior and generated output are part of the v1 hardening scope and must stay aligned with the repository's canonical docs

## Features

- **Machine-First Design**: JSON/YAML/Text output formats (default: JSON)
- **Non-Interactive**: All operations via flags, no prompts
- **Predictable**: Clear exit codes (0=success, 1=error, 2=warning)
- **Composable**: Works seamlessly with jq, grep, pipes
- **Automation-Ready**: Perfect for CI/CD and code agents

Global flags are parsed before the command token. Prefer
`plumego --format json <command>` over placing global flags after the command.
Command help follows the selected output format: JSON/YAML help uses the stable
command-result envelope with the help text under `data.help`, while text help
prints the human-readable usage directly.

## Output Contract

Machine formats are line-oriented and use two explicit shapes:

- Command results use the command-result envelope:
  `status`, `message`, optional `exit_code`, and optional `data`. This applies
  to success, warning, error, help, and any direct `Print(string)` call in JSON
  or YAML mode. A raw string is encoded as `data.value`; it is never emitted as a
  bare JSON string or bare YAML scalar.
- Streaming output uses event objects with `event`, optional `level`,
  `message`, `time`, and `data`. `plumego dev` emits these events in JSON/YAML
  modes after startup because it can produce multiple build, runner, watcher,
  and log updates over time. Event objects are not wrapped in command-result
  envelopes.

Text mode is human-oriented. Command results print a status line plus an
optional JSON data block, while streaming events print the event message with a
level prefix for warnings, errors, and debug output.

## Installation

### Supported Source Install

```bash
# Clone the repository
git clone https://github.com/birdordev/plumego.git
cd plumego/cmd/plumego

# Build and install
go build -o plumego .
sudo mv plumego /usr/local/bin/

# Or just build locally
go build -o ../../bin/plumego .
```

Keep local development binaries under the repository-level `bin/` directory
(`go build -o ../../bin/plumego .`). The source-directory fallback
`cmd/plumego/plumego` is ignored only as a cleanup guard; do not use it as the
normal build target, because it is easy to run a stale binary from the module
directory.

The CLI currently lives in an independent nested module with a local
`replace github.com/birdordev/plumego => ../..` directive for repository
development. Until the release checklist verifies tagged module installation,
the supported install path is building from a checked-out Plumego repository.

### Tagged Install Verification

Before release notes can advertise `go install
github.com/birdordev/plumego/cmd/plumego@<tag>`, maintainers must run the tagged
install smoke test from `docs/release/PRE_V1_RELEASE_CHECKLIST.md` against the
actual release tag and record the result.

### Verify Installation

```bash
plumego --help
```

## Quick Start

### Create a New Project

```bash
# Create a minimal project
plumego new myapp

# Create the canonical reference-style layout
plumego new myapp --template canonical

# Create an API server
plumego new myapi --template api

# Create a scenario-profile scaffold
plumego new rest-api --template rest-api
plumego new tenant-api --template tenant-api
plumego new edge-api --template gateway
plumego new realtime-api --template realtime
plumego new ai-api --template ai-service
plumego new ops-api --template ops-service

# Create with custom module path
plumego new myapp --template fullstack --module github.com/myorg/myapp
```

Supported templates are: `canonical`, `minimal`, `api`, `fullstack`,
`microservice`, `rest-api`, `tenant-api`, `gateway`, `realtime`, `ai-service`,
and `ops-service`.

The `canonical` template is aligned with `reference/standard-service`: stable
root imports only, explicit config loading, explicit route registration in
`internal/app/routes.go`, `main.run -> app.Start(ctx)` lifecycle ownership,
local handler DTOs, `internal/domain/<name>` business examples, and no `x/*`
capability wiring by default.

The `minimal`, `fullstack`, and `microservice` template names are stable CLI
inputs, but their v1 hardening output now uses the same canonical bootstrap
rather than legacy `internal/httpapp`, frontend, or container scaffolds.

When `plumego new` runs from a Plumego source checkout, generated `go.mod` files
include a local `replace github.com/birdordev/plumego => <checkout>` directive so
pre-release source workflows can build against the checked-out framework.

The `api` template starts from the same canonical bootstrap and adds a minimal
`x/rest` users resource profile under `internal/resource`. It keeps resource
route wiring explicit in `internal/app/routes.go`; `x/rest` is not part of the
default `canonical` template.

Scenario profiles keep the same canonical bootstrap and add explicit optional
capability wiring. They include runnable scenario routes plus
`internal/scenario/profile.go` capability markers for the selected family:

| Template | Capability profile |
| --- | --- |
| `rest-api` | `x/rest` users resource under `/api/users` |
| `tenant-api` | `x/tenant/resolve`, `x/tenant/policy`, `x/tenant/quota`, `x/tenant/ratelimit` on `/api/models` |
| `gateway` | `x/gateway` loopback proxy under `/edge` |
| `realtime` | `x/websocket` hub metrics under `/realtime/metrics`, plus `x/messaging` marker |
| `ai-service` | offline `x/ai/provider`, `x/ai/session`, and `x/ai/tool` demo under `/ai/demo`, plus `x/ai/streaming` marker |
| `ops-service` | protected `x/observability` metrics under `/ops/metrics` and protected admin boundary summary under `/ops/admin` using `x/observability/ops` DTOs |

These profiles do not install secrets, live provider credentials, hidden
globals, or default `x/observability/devtools` routes.

### Generate Code

```bash
# Generate middleware
plumego generate middleware RateLimit

# Generate REST handlers
plumego generate handler User --methods GET,POST,PUT,DELETE --with-tests

# Generate a model
plumego generate model Invoice --with-validation

# Generate an OpenAPI 3.1 document from a Plumego app route table
plumego generate spec --dir reference/with-rest --format yaml --output openapi.yaml
```

Generated handlers default to `internal/handler`, middleware defaults to
`internal/middleware`, and models default to `internal/domain/<name>`.
`plumego generate spec` uses a temporary helper process so the CLI module does
not import optional OpenAPI generation packages directly. By default it follows
the canonical app layout under `internal/app` and reads optional operation hints
from `plumego.spec.yaml` in the project root.

### Development Server

```bash
# Start dev server with hot reload
plumego dev

# Custom port
plumego dev --addr :3000
```

The development dashboard APIs return structured Plumego error responses for
local tooling failures. Public error codes are uppercase stable identifiers, and
default messages avoid exposing local filesystem, parser, build, or `go list`
diagnostics.
Hot reload uses a dependency-free polling watcher. The watcher validates
positive debounce and poll durations, polls every 500ms by default, and remains a
best fit for local projects rather than very large repository trees.

`plumego inspect --auth` accepts the complete `Authorization` header value, for
example `--auth "Bearer <token>"`. Inspect response bodies are capped at 10 MiB;
larger responses fail with a structured error instead of being truncated.

### Add Community Extensions

```bash
plumego add github.com/acme/plumego-cache --version v0.1.0
```

`plumego add <module-path>` resolves the requested module with `go list`,
downloads it with `go mod download`, reads `community-extension.yaml`, validates
the schema contract, runs `go vet ./...`, scans for imports declared in
`forbidden_imports`, and only then runs `go get <module-path>@<version>`.

On success, the command prints a compliance report with `module_path`,
`version`, `manifest_path`, `fields_validated`, `checks_passed`, and
`module_added`. On schema, vet, forbidden-import, or network failure, it exits
with code 1 before modifying `go.mod`.

## Commands

The v1 CLI surface currently targets these 13 commands:

1. **new** - Create projects from templates
2. **generate** - Generate middleware, handlers, models
3. **add** - Validate and add community extension modules
4. **dev** - Development server with hot reload
5. **check** - Health and security validation
6. **config** - Configuration management
7. **routes** - Route analysis and inspection
8. **migrate** - Offline migration file creation plus runtime migration commands for custom driver builds
9. **test** - Enhanced test runner
10. **build** - Build with optimizations
11. **inspect** - Runtime inspection
12. **serve** - Local static file preview server
13. **version** - Build and version metadata

See module notes: [MODULE.md](./MODULE.md)

`plumego config show` redacts sensitive values by default. Use
`--show-secrets` only for trusted local debugging when raw values are required.
The CLI parser supports common `.env` forms such as `export KEY=value`, quoted
values, and inline comments. Missing `.env` files remain optional, but invalid
env-file syntax is reported by config validation and security checks.

## CI/CD Integration

```yaml
# GitHub Actions example
- name: Install Plumego CLI
  run: |
    cd cmd/plumego
    go build -o $GITHUB_WORKSPACE/bin/plumego .

- name: Health Check
  run: plumego --format json check --dir .

- name: Run Tests
  run: plumego test --race --cover --format json
```

Stable CLI smoke coverage includes generating a canonical project, running
`go mod tidy`, then exercising `plumego build`, `plumego test`, and
`plumego check` against that generated project. Release candidates should also
smoke JSON/YAML command-result output and text help output.

For fast local command-contract feedback, run `go test -short ./commands` from
`cmd/plumego`; this is the fast contract gate and skips the generated-project
smoke path. Full CLI confidence still requires `go test ./...` from
`cmd/plumego`, which includes the slow generated-project smoke layer.

`plumego test --cover` uses a temporary coverage profile by default so it does
not overwrite `coverage.out` in the project root. Use `--coverprofile <path>`
when CI needs to keep the raw profile.

`plumego routes` is a static best-effort analyzer for direct literal route
registrations such as `app.Get("/health", handler)`. It extracts method, path,
handler name, file, and line from direct selector calls with literal string path
arguments. It does not infer route groups, middleware chains, variable-built
paths, wrapper registration helpers, or routes hidden behind factory functions.
It supports sorting by `path` or `method`; route group and middleware filtering
are explicit unsupported options.

`plumego serve` runs a local static file server with signal-aware graceful
shutdown. It is intended for local file previews, not production hosting.

`plumego migrate create` is an offline migration-file generator and works
without a database driver. Runtime operations (`migrate status`, `migrate up`,
and `migrate down`) require a CLI build that imports the target `database/sql`
driver; the bundled source build does not add database driver dependencies. If
the requested driver is not registered, the command fails before opening a
connection or loading migration state. `migrate status` reads the existing
`schema_migrations` table without creating it; `migrate up` and `migrate down`
ensure the table before changing migration state. No-op `up` and `down` results
return the warning envelope with exit code 2.
Runtime commands read `plumego.migrate.yaml` when present, with `driver`,
`db_url`, and `dir` keys. Command flags override config-file values.

## Exit Codes

- `0` = Success
- `1` = General error
- `2` = Warning or degraded result
- `3` = Resource conflict

## License

Same as plumego core - see [LICENSE](../../LICENSE).
