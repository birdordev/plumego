# Getting Started

Plumego is a standard-library-first HTTP toolkit for Go.

Use this page for the smallest runnable example. For the canonical application
layout, read `reference/standard-service` immediately after this page.
For the broader adoption sequence, read `docs/start/adoption-path.md`.

## Requirements

- Go 1.26 or later
- no external runtime dependencies in the main module

Install:

```bash
go get github.com/birdordev/plumego@latest
```

## Smallest Runnable Example

Create `main.go`:

```go
package main

import (
	"log"
	"net/http"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/contract"
)

func main() {
	app := plumego.New()

	app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]string{
			"message": "pong",
		}, nil); err != nil {
			http.Error(w, "write response", http.StatusInternalServerError)
		}
	}))

	log.Println("server started at :8080")
	log.Fatal(http.ListenAndServe(":8080", app))
}
```

Run it:

```bash
go run main.go
```

Open `http://localhost:8080/ping`.

`plumego.New()` uses `DefaultConfig` (`:8080`, 30 s timeouts, HTTP/2 on).
For a non-default address or TLS, use `plumego.NewWithConfig`:

```go
package main

import (
	"log"
	"net/http"

	"github.com/birdordev/plumego"
	"github.com/birdordev/plumego/contract"
)

func main() {
	cfg := plumego.DefaultConfig()
	cfg.Addr = ":9090"
	app := plumego.NewWithConfig(cfg)
	app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, 200, map[string]string{"status": "ok"}, nil)
	}))
	log.Fatal(http.ListenAndServe(cfg.Addr, app))
}
```

For explicit lifecycle control — `Prepare`, `Server`, `Shutdown` — use `core.New` directly (see the production example below).

## Production-Style Canonical Example

Use this when you need request ID, panic recovery, graceful shutdown, and
canonical lifecycle wiring. Use `core.New` directly for explicit logger
injection:

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
)

func main() {
	if err := run(); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := core.DefaultConfig()
	cfg.Addr = ":8080"
	app := core.New(cfg, core.AppDependencies{Logger: plog.NewLogger()})

	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		return err
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: app.Logger()})
	if err != nil {
		return err
	}
	if err := app.Use(
		requestid.Middleware(),
		recoveryMw,
		accesslogMw,
	); err != nil {
		return err
	}

	if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, map[string]string{
			"message": "pong",
		}, nil)
	})); err != nil {
		return err
	}

	if err := app.Prepare(); err != nil {
		return err
	}
	srv, err := app.Server()
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = app.Shutdown(context.Background())
	}()

	log.Println("server started at :8080")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

## Bootstrap Progression

As your application grows, three common bootstrap patterns emerge:

1. **`plumego.New()`** — zero-config hello-world: best for learning and examples.
2. **`core.New` + inline `Prepare/Server/Shutdown`** — explicit lifecycle control shown above: needed for signal handling and custom logger injection.
3. **`app.New` + `RegisterRoutes` wrapper** — canonical app layout (see `reference/standard-service`): recommended for production services that separate bootstrap, routing, and middleware wiring.

When the inline example stops being enough, copy structure from `reference/standard-service` rather than extending `main.go`.

## Basic Routing

Plumego uses the standard `net/http` handler shape.

```go
if err := app.Get("/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("list users"))
})); err != nil {
	log.Fatalf("register route: %v", err)
}

if err := app.Post("/users", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("create user"))
})); err != nil {
	log.Fatalf("register route: %v", err)
}
```

For path parameters, use `plumego.Param` (or `router.Param` when importing
`router` directly):

```go
if err := app.Get("/users/:id", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	id := plumego.Param(r, "id")
	_, _ = w.Write([]byte(id))
})); err != nil {
	log.Fatalf("register route: %v", err)
}
```

## Choose Your Next Module

After the smallest example works, keep the app layout from
`reference/standard-service` and add only the capability family you need:

| Goal | First module |
| --- | --- |
| Standard JSON API with explicit handlers | stable roots: `core`, `router`, `contract`, `middleware` |
| Reusable CRUD/resource conventions | `x/rest` |
| Tenant resolution, policy, quota, and isolation | `x/tenant` |
| Reverse proxy, rewrite, balancing, and edge transport | `x/gateway`, then `x/gateway/discovery` only when dynamic backend lookup is required |
| WebSocket transport | `x/websocket` |
| Messaging workflows | `x/messaging` |
| Inbound webhook verification or outbound webhook delivery | `x/messaging/webhook`, starting from `x/messaging` when the task is broader than transport mechanics |
| File upload, download, and temporary URL transport | `x/fileapi`, with storage and metadata implementations in `x/data/file` |
| Reusable circuit breaker or rate-limit primitives | `x/resilience` |
| AI providers, sessions, streaming, and tools | `x/ai/provider`, `x/ai/session`, `x/ai/streaming`, `x/ai/tool` |
| Observability export, protected diagnostics, or local debug endpoints | `x/observability`, `x/observability/ops`, or `x/observability/devtools` depending on the surface |
| gRPC + HTTP service hosting or outbound RPC client pooling | `x/rpc` |
| OpenAPI 3.1 document generation from registered routes | `x/openapi` |
| Explicit JSON binding and caller-owned request validation | `x/validate` |

Do not start a new application layout from an `x/*` package. Treat extensions as
explicit additions to the canonical app wiring.

For generated project scaffolds, keep `canonical` as the default template and
select a scenario profile only when you want explicit optional capability
wiring. Baseline templates are `canonical`, `minimal`, `fullstack`, and
`microservice`. Scenario templates are `api`, `rest-api`, `tenant-api`, `gateway`,
`realtime`, `ai-service`, and `ops-service`; each is accepted by
`plumego new --template`.

The `canonical` scaffold follows the same runtime wiring as
`reference/standard-service`, but it is shaped as a generated project: the
entrypoint is `cmd/app/main.go`, project-local files are generated, and
reference-only tests are not copied.

```bash
plumego new myapi --template rest-api
plumego new tenantapi --template tenant-api
plumego new edge --template gateway
plumego new realtime --template realtime
plumego new ai --template ai-service
plumego new ops --template ops-service
```

## Canonical Next Reads

Read these next, in order:

1. `docs/start/adoption-path.md`
2. `reference/standard-service`
3. `docs/README.md`
4. `docs/reference/canonical-style-guide.md`
5. `docs/concepts/agent-first-repo-blueprint.md`
6. `specs/task-routing.yaml`
7. the target module's `module.yaml`

## Notes

- `reference/standard-service` is the canonical application layout.
- `docs/README.md` is the entrypoint for the human-readable docs surface.
- `x/*` packages are optional capability families, not the default bootstrap path.
- Prefer explicit route wiring and explicit middleware registration.
- Prefer `contract.WriteError` and `contract.WriteResponse` for JSON APIs.
- When the example stops being enough, copy structure from `reference/standard-service` instead of inventing a new bootstrap shape.

## Troubleshooting

If something is not working as expected, see
[`docs/start/troubleshooting.md`](./troubleshooting.md). Common issues include:
routes not matching after `Prepare`, middleware order, empty
`router.Param` values, and JWT verification failures.
