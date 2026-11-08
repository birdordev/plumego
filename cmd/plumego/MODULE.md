# Plumego CLI - Independent Module

## Overview

The Plumego CLI (`cmd/plumego`) is an **independent Go module** with its own `go.mod` file. This separation ensures that CLI-specific dependencies (like YAML support) don't affect the core plumego library.

## Module Structure

```
plumego/                           # Main module
├── go.mod                         # Core plumego (no extra dependencies)
└── cmd/plumego/                   # CLI module
    ├── go.mod                     # Independent module with CLI dependencies
    ├── go.sum                     # CLI dependency checksums
    └── ...                        # CLI source code
```

## Why Independent Module?

### Benefits:
1. **Dependency Isolation**: CLI dependencies (like `gopkg.in/yaml.v3`) don't pollute the core plumego module
2. **Cleaner Core**: Projects using plumego as a library get zero extra dependencies
3. **Flexible Tooling**: CLI can use any dependencies without affecting core
4. **Better Compatibility**: Core library remains ultra-lightweight

### Dependencies:
- **Core plumego**: `go 1.26.0` - **zero external dependencies**
- **CLI**: `go 1.26.0` - `gopkg.in/yaml.v3` for YAML output support

## How It Works

### cmd/plumego/go.mod:
```go
module github.com/birdordev/plumego/cmd/plumego

go 1.26.0

// Use local plumego package
replace github.com/birdordev/plumego => ../..

require (
    github.com/birdordev/plumego v0.0.0-00010101000000-000000000000
    gopkg.in/yaml.v3 v3.0.1
)
```

The `replace` directive tells Go to use the local plumego code instead of fetching it remotely.

## Building

### From Repository Root:
```bash
# Build CLI
cd cmd/plumego
go build -o ../../bin/plumego .

# Or use the project bin directory
go build -o /home/user/plumego/bin/plumego .
```

Keep development builds outside the module source directory. The supported local
artifact location is the repository-level `bin/plumego` path, for example
`go build -o ../../bin/plumego .` from `cmd/plumego`. The ignored
`cmd/plumego/plumego` path exists only as a cleanup guard for older workflows;
do not use it as the normal target because it is easy to run a stale binary.

## Stable Command Surface

The planned v1 CLI command surface is:

- `new`
- `generate`
- `dev`
- `routes`
- `check`
- `config`
- `migrate`
- `test`
- `build`
- `inspect`
- `serve`
- `version`

Top-level help and `cmd/plumego/README.md` must stay synchronized with this
list. Each stable command must provide command-specific help through
`plumego <command> --help` without requiring command execution side effects.
For machine output formats, help is returned as a command-result envelope with
the rendered help text in `data.help`; text output prints the help body directly.

Stable smoke coverage for this module must include:

- source build of the CLI module
- JSON and YAML command-result output for `version`
- text command help for at least one command
- generated canonical project workflow through `new`, `build`, `test`, and `check`

Use `go test -short ./commands` as the fast command-contract gate. It covers
parsing, help, envelope shape, and command error contracts while skipping the
generated-project smoke path. Use `go test ./...` from `cmd/plumego` as the full
module gate; it includes the slow generated-project smoke layer.

### Install Globally:
```bash
cd cmd/plumego
go install
```

This installs `plumego` to `$GOPATH/bin` (or `$HOME/go/bin`).

## Development

### Adding Dependencies to CLI:
```bash
cd cmd/plumego
go get <package>
go mod tidy
```

Dependencies are **only** added to `cmd/plumego/go.mod`, not the core.

### Adding Dependencies to Core:
```bash
cd /home/user/plumego
go get <package>
go mod tidy
```

Core dependencies affect both core and CLI (via replace directive).

## Testing

### Test CLI Builds:
```bash
cd cmd/plumego
go build -o ../../bin/plumego .
```

### Test Core Builds (without CLI deps):
```bash
cd /home/user/plumego
go build ./...   # Should not include cmd/plumego
```

### Verify Independence:
```bash
# Check core has no YAML dependency
cd /home/user/plumego
grep yaml go.mod  # Should return nothing

# Check CLI has YAML dependency
cd cmd/plumego
grep yaml go.mod  # Should show gopkg.in/yaml.v3
```

## CI/CD Considerations

### Building in CI:
```yaml
# Build core library
- name: Build core
  run: |
    cd /home/user/plumego
    go build ./core
    go build ./router
    go build ./middleware
    # ... other packages

# Build CLI (separate step)
- name: Build CLI
  run: |
    cd /home/user/plumego/cmd/plumego
    go build -o plumego .
```

### Testing in CI:
```yaml
# Test core
- name: Test core
  run: |
    cd /home/user/plumego
    go test ./...

# Test CLI
- name: Test CLI
  run: |
    cd /home/user/plumego/cmd/plumego
    go test ./...
```

## Release Process

When releasing the CLI:

1. **Tag the main repository** (includes CLI sources):
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

2. **Verify tagged CLI installation** before advertising it:
   ```bash
   GOBIN="$(mktemp -d)" go install github.com/birdordev/plumego/cmd/plumego@v1.0.0
   "$(ls "$GOBIN"/plumego)" version
   ```

   This repository uses a nested CLI module with a local `replace` directive for
   development. Tagged install must be treated as a release gate, not an assumed
   property of the repository layout.

3. **If tagged installation has not been verified**, document source install as
   the supported path:
   ```bash
   git clone https://github.com/birdordev/plumego.git
   cd plumego/cmd/plumego
   go build -o plumego .
   ```

4. **Projects use core** with:
   ```go
   import "github.com/birdordev/plumego/core"
   ```
   ```bash
   go get github.com/birdordev/plumego@v1.0.0
   ```

## FAQ

### Q: Why not use build tags?
**A**: Build tags would still require the dependencies to be listed in the main `go.mod`. This approach completely separates them.

### Q: Can I use the CLI as a library?
**A**: Yes, but you'll get the YAML dependency. If you only need CLI functionality without YAML output, we recommend copying the relevant code.

### Q: Does this affect go get?
**A**: No. Users can still `go get github.com/birdordev/plumego` and get the core library without CLI dependencies.

### Q: What about the replace directive?
**A**: It is required for local development and building from a repository
checkout. Tagged `go install` is only a supported user install path after the
release checklist verifies it against the actual tag.

## Verification Commands

```bash
# Verify core module has no extra dependencies
cd /home/user/plumego
go mod graph | grep -v "github.com/birdordev/plumego"
# Should output nothing or only Go standard library

# Verify CLI module dependencies
cd /home/user/plumego/cmd/plumego
go mod graph | grep yaml
# Should show: gopkg.in/yaml.v3

# Build and test everything
cd /home/user/plumego
go build ./core ./router ./middleware  # Core packages
cd cmd/plumego
go build .  # CLI
./plumego --help  # Test it works
```

## Summary

This independent module approach ensures:
- ✅ Core plumego remains dependency-free
- ✅ CLI can use any tooling dependencies
- ✅ Clean separation of concerns
- ✅ No breaking changes for existing users
- ✅ Simple and maintainable structure

The `replace` directive makes local development seamless while keeping the modules logically separate.
