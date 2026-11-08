# Community Extension Authoring

This guide defines the contract for community-authored Plumego extensions. It
is for packages distributed outside the main Plumego repository and installed by
applications with `plumego add`.

Community extensions are explicit Go modules. They are not a hosted registry,
not an auto-wiring system, and not a way to extend Plumego's stable kernel at
runtime. A compatible extension exposes ordinary Go constructors and handlers,
keeps route mounting in the caller's application, avoids package-level mutable
state, and ships a `community-extension.yaml` file at the module root.

## Schema Contract

The machine-readable schema lives at
`specs/community-extension.schema.yaml`.

| Field | Required value |
| --- | --- |
| `name` | Short package name, such as `rpc` or `cache` |
| `module_path` | Full Go module path published for `go get` |
| `status` | One of `experimental`, `beta`, or `ga` |
| `handler_shape` | Exactly `func(http.ResponseWriter, *http.Request)` |
| `test_commands` | At least one command contributors can run locally |
| `forbidden_imports` | Must include every stable-root package path listed in the schema |
| `no_init_side_effects` | Must be `true`; `init()` must not register routes, clients, or global state |
| `no_globals` | Must be `true`; use constructors and caller-owned dependencies |
| `owner` | Team, maintainer, or contact responsible for compatibility |

The `status` field communicates maturity only for the extension module itself.
It does not promote any Plumego `x/*` family and does not create a compatibility
promise beyond the extension author's published policy.

## community-extension.yaml Example

This example models a community RPC-style extension published outside this
repository: experimental status, explicit handler compatibility, and local
validation commands. The in-repository `x/rpc` package is first-party code in
the main module; external community modules using this shape should keep direct
stable-root integration out of the published module if they list those imports
as forbidden.

```yaml
name: rpc
module_path: github.com/birdordev/plumego/x/rpc
status: experimental
handler_shape: "func(http.ResponseWriter, *http.Request)"
test_commands:
  - go test -race -timeout 60s ./...
  - go vet ./...
forbidden_imports:
  - github.com/birdordev/plumego/core
  - github.com/birdordev/plumego/router
  - github.com/birdordev/plumego/contract
  - github.com/birdordev/plumego/middleware
  - github.com/birdordev/plumego/security
  - github.com/birdordev/plumego/store
  - github.com/birdordev/plumego/health
  - github.com/birdordev/plumego/log
  - github.com/birdordev/plumego/metrics
no_init_side_effects: true
no_globals: true
owner: rpc
```

## Compliance Checklist

1. `name`
   Pass: `name: rpc`
   Fail: missing `name`, or `name: ""`

2. `module_path`
   Pass: `module_path: github.com/acme/plumego-rpc`
   Fail: `module_path` does not match the module passed to `plumego add`

3. `status`
   Pass: `status: experimental`
   Fail: `status: preview`

4. `handler_shape`
   Pass: `handler_shape: "func(http.ResponseWriter, *http.Request)"`
   Fail: `handler_shape: "func(*Context) error"`

5. `test_commands`
   Pass: at least one command, for example `go test ./...`
   Fail: omitted or empty list

6. `forbidden_imports`
   Pass: includes all stable-root import paths from the schema
   Fail: omits `github.com/birdordev/plumego/core`, or imports a forbidden path in
   extension code

7. `no_init_side_effects`
   Pass: constructors are called explicitly by the application
   Fail: `init()` registers handlers, clients, metrics, or background workers

8. `no_globals`
   Pass: dependencies are passed through constructors or options
   Fail: package-level mutable clients, stores, registries, or default servers

## Publishing Your Extension

1. Put `community-extension.yaml` at the Go module root.
2. Keep public examples on `func(http.ResponseWriter, *http.Request)` and
   `http.Handler` compatible surfaces.
3. Export constructors or option structs; leave route registration to the
   caller's `main` or app wiring package.
4. Tag a version and make sure the module is visible to `go list -m -json`.
5. Confirm pkg.go.dev can index the module.
6. From a consuming application, run:

```bash
plumego add github.com/acme/plumego-rpc --version v0.1.0
```

`plumego add` resolves the module, downloads it through Go module tooling,
validates `community-extension.yaml`, runs `go vet ./...`, scans imports
against `forbidden_imports`, and runs `go get` only after the compliance checks
pass. It does not mount routes, edit application code, or register globals.

## Validation

From a Plumego source checkout, validate a local extension directory with:

```bash
go run ./internal/checks/community-extension /path/to/extension
```

Exit code `0` means the manifest satisfies
`specs/community-extension.schema.yaml`. Exit code `1` includes a structured
list of missing or invalid fields, such as an unsupported `status`, an omitted
`test_commands` list, or `no_init_side_effects: false`.

For a published module, prefer the end-to-end consumer check:

```bash
plumego add github.com/acme/plumego-rpc
```

If `go list` fails, check network access, `GOPROXY`, and whether the module path
or version has been published.

## Maintaining Compatibility

Use the same maturity vocabulary as Plumego's extension model:

| Status | Meaning |
| --- | --- |
| `experimental` | API may change; consumers own upgrade risk |
| `beta` | API should remain stable within the current major version |
| `ga` | Public API follows the extension author's compatibility policy |

Breaking changes include removing exported symbols, changing handler or
constructor signatures, changing required configuration fields, weakening
fail-closed security behavior, or changing response/error shapes documented by
the extension.

Non-breaking changes include adding optional constructors, adding new helpers,
fixing bugs while preserving documented behavior, and adding new test commands.

Do not infer maturity from download count or package age. Record status in
`community-extension.yaml`, document behavior that consumers can rely on, and
keep validation commands current as the extension evolves.
