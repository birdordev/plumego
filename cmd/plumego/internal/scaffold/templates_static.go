package scaffold

import (
	"fmt"
)

func getScenarioProfileContent(template string) string {
	switch template {
	case "rest-api":
		return `// Package scenario documents the selected scaffold profile.
package scenario

import "github.com/birdordev/plumego/x/rest"

// Name identifies the scaffold profile.
const Name = "rest-api"

// Capabilities returns the optional Plumego capability families this profile uses.
func Capabilities() []string {
	spec := rest.DefaultResourceSpec("users").WithPrefix("/api/users")
	return []string{"x/rest", spec.Prefix}
}
`
	case "tenant-api":
		return `// Package scenario documents the selected scaffold profile.
package scenario

import (
	"github.com/birdordev/plumego/x/tenant/policy"
	"github.com/birdordev/plumego/x/tenant/quota"
	"github.com/birdordev/plumego/x/tenant/ratelimit"
	"github.com/birdordev/plumego/x/tenant/resolve"
)

// Name identifies the scaffold profile.
const Name = "tenant-api"

// Capabilities returns the optional Plumego capability families this profile uses.
func Capabilities() []string {
	_ = resolve.Middleware
	_ = policy.Middleware
	_ = quota.Middleware
	_ = ratelimit.Middleware
	return []string{"x/tenant/resolve", "x/tenant/policy", "x/tenant/quota", "x/tenant/ratelimit"}
}
`
	case "gateway":
		return `// Package scenario documents the selected scaffold profile.
package scenario

import "github.com/birdordev/plumego/x/gateway"

// Name identifies the scaffold profile.
const Name = "gateway"

// Capabilities returns the optional Plumego capability families this profile uses.
func Capabilities() []string {
	_ = gateway.RegisterProxy
	return []string{"x/gateway"}
}
`
	case "realtime":
		return `// Package scenario documents the selected scaffold profile.
package scenario

import (
	"github.com/birdordev/plumego/x/messaging"
	"github.com/birdordev/plumego/x/websocket"
)

// Name identifies the scaffold profile.
const Name = "realtime"

// Capabilities returns the optional Plumego capability families this profile uses.
func Capabilities() []string {
	_ = messaging.New
	return []string{"x/websocket", "x/messaging"}
}
`
	case "ai-service":
		return `// Package scenario documents the selected scaffold profile.
package scenario

import (
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/session"
	"github.com/birdordev/plumego/x/ai/streaming"
	"github.com/birdordev/plumego/x/ai/tool"
)

// Name identifies the scaffold profile.
const Name = "ai-service"

// Capabilities returns the optional Plumego capability families this profile uses.
func Capabilities() []string {
	_ = provider.NewMockProvider
	_ = session.NewManager
	_ = streaming.NewStreamManager
	_ = tool.NewRegistry
	return []string{"x/ai/provider", "x/ai/session", "x/ai/streaming", "x/ai/tool"}
}
`
	case "ops-service":
		return `// Package scenario documents the selected scaffold profile.
package scenario

import (
	"github.com/birdordev/plumego/x/observability"
	"github.com/birdordev/plumego/x/observability/ops"
)

// Name identifies the scaffold profile.
const Name = "ops-service"

// Capabilities returns the optional Plumego capability families this profile uses.
func Capabilities() []string {
	_ = observability.Configure
	_ = ops.New
	return []string{"x/observability", "x/observability/ops"}
}
`
	default:
		return `// Package scenario documents the selected scaffold profile.
package scenario

// Name identifies the scaffold profile.
const Name = "custom"

// Capabilities returns optional Plumego capability families this profile uses.
func Capabilities() []string {
	return nil
}
`
	}
}

func getEnvExampleContent() string {
	return `APP_ADDR=:8080
APP_DEBUG=false
APP_SHUTDOWN_TIMEOUT_MS=5000
APP_MAX_BODY_BYTES=10485760
`
}

func getGitignoreContent() string {
	return `# Binaries
*.exe
*.exe~
*.dll
*.so
*.dylib
bin/
dist/

# Test coverage
*.out
coverage.html

# Environment
.env
.env.local

# IDE
.vscode/
.idea/
*.swp
*.swo
*~

# OS
.DS_Store
Thumbs.db
`
}

func getReadmeContent(name, template string) string {
	return fmt.Sprintf(`# %s

A plumego application built with the **%s** template.

## Getting Started

### Install dependencies
`+"```bash"+`
go mod tidy
`+"```"+`

### Run development server
`+"```bash"+`
plumego dev
# or
go run ./cmd/app
`+"```"+`

### Run tests
`+"```bash"+`
plumego test
# or
go test ./...
`+"```"+`

### Build
`+"```bash"+`
plumego build
# or
go build -o bin/app ./cmd/app
`+"```"+`

## Documentation

See [Plumego documentation](https://github.com/birdordev/plumego) for more information.
`, name, template)
}

func getMakefileContent() string {
	return `.PHONY: gates test vet fmt-check build run tidy

gates: fmt-check vet test build

test:
	go test -timeout 20s ./...

vet:
	go vet ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

build:
	go build -o bin/app ./cmd/app

run:
	go run ./cmd/app

tidy:
	go mod tidy
`
}

func getCIWorkflowContent() string {
	return `name: ci

on:
  pull_request:
  push:
    branches:
      - main

permissions:
  contents: read

jobs:
  gates:
    runs-on: ubuntu-latest
    timeout-minutes: 20

    steps:
      - name: Checkout
        uses: actions/checkout@v6

      - name: Setup Go
        uses: actions/setup-go@v6
        with:
          go-version-file: go.mod

      - name: Download dependencies
        run: go mod download

      - name: Run gates
        run: make gates
`
}

func getAgentsContent(name string) string {
	return fmt.Sprintf(`# AGENTS.md - %s

Operational guide for AI coding agents working in this Plumego application.

## Rules

- Preserve `+"`net/http`"+` handler compatibility.
- Keep route wiring explicit: one method, one path, one handler per line.
- Decode JSON in handlers with `+"`json.NewDecoder(r.Body).Decode(&dst)`"+`.
- Write success responses through `+"`contract.WriteResponse`"+`.
- Write API errors through `+"`contract.WriteError`"+`.
- Add dependencies only when the application code needs them directly.
- Do not introduce hidden globals, `+"`init()`"+` registration, or context-based service lookup.
- Keep middleware in the standard `+"`func(http.Handler) http.Handler`"+` shape.
- Run `+"`make gates`"+` before handing off code changes.

## Project Layout

- `+"`cmd/app`"+`: process entrypoint.
- `+"`internal/app`"+`: dependency assembly and route registration.
- `+"`internal/config`"+`: configuration loading and validation.
- `+"`internal/handler`"+`: HTTP handlers.

Prefer constructor injection and small local interfaces over package globals.
`, name)
}

func getClaudeContent(name string) string {
	return fmt.Sprintf(`# CLAUDE.md - %s

This project follows the same operational contract as `+"`AGENTS.md`"+`.

Use this file for assistant-specific notes only. Keep durable engineering rules
in `+"`AGENTS.md`"+` so every agent sees the same source of truth.

## Common Commands

`+"```bash"+`
make gates
make test
make build
go run ./cmd/app
`+"```"+`

## Implementation Notes

- Keep application wiring in `+"`internal/app`"+`.
- Keep business or resource-specific behavior outside middleware.
- Prefer typed response structs for public JSON APIs.
- Keep validation explicit at handler or service boundaries.
`, name)
}
