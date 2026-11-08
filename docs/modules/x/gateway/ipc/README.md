# x/gateway/ipc — Inter-Process Communication

> **Import path:** `github.com/birdordev/plumego/x/gateway/ipc` — sub-package of [`x/gateway`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

**Purpose:** IPC transport helpers within the `x/gateway` family. Provides framed client/server, connection pools, stream channels, heartbeat, and reconnect primitives for low-latency in-process or same-host communication.

**Status:** Experimental — API may change. Start IPC-facing feature discovery from [`x/gateway`](../README.md) before opening this package directly.

---

## First files to read

- `x/gateway/ipc/module.yaml`
- `x/gateway/ipc/server.go` — `Server` and `NewServer`
- `x/gateway/ipc/client.go` — `Client`, `Dial`, `DialWithContext`
- `x/gateway/module.yaml`

---

## Key types

| Type / Constructor | Description |
|---|---|
| `Server` / `NewServer` | IPC server with configurable rate limiting |
| `Client` / `Dial` | Single IPC client connection |
| `FramedClient` / `NewFramedClient` | Length-prefixed framed message client |
| `Pool` / `NewPool` | Connection pool with configurable size and timeouts |
| `StreamClient` / `NewStreamClient` | Streaming channel over IPC connection |
| `DialWithReconnect` | Client with automatic reconnection and backoff |
| `NewHeartbeatClient` | Client with configurable keepalive heartbeat |
| `Addr` / `NewAddr` | IPC address abstraction |

---

## Boundary rules

- Treat as a subordinate primitive; start new IPC-facing gateway work from `x/gateway`.
- Keep IPC internals self-contained with narrow adapters.
- Do not require `core` knowledge or inject business logic.
- Process-wide side effects (background goroutines) must be tied to explicit lifecycle; no `init()` registration.

---

## Validation

```bash
go test -race -timeout 60s ./x/gateway/ipc/...
go vet ./x/gateway/ipc/...
```
