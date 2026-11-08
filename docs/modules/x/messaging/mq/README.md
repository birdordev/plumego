# x/messaging/mq — Durable Message Queue

> **Import path:** `github.com/birdordev/plumego/x/messaging/mq` — sub-package of [`x/messaging`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

**Purpose:** Durable queue primitives and worker coordination within the `x/messaging` family. Provides in-process broker, persistent task queue, worker pool, dead-letter handling, deduplication, and TTL-based expiry.

**Status:** Experimental — API may change. Start app-facing messaging feature discovery from [`x/messaging`](../README.md) before opening this package directly.

---

## First files to read

- `x/messaging/mq/module.yaml`
- `x/messaging/mq/broker.go` — `InProcBroker`, `NewInProcBroker`, `NewInProcBrokerE`
- `x/messaging/mq/queue.go` — `TaskQueue`, `NewTaskQueue`
- `x/messaging/module.yaml`

---

## Key types

| Type / Constructor | Description |
|---|---|
| `InProcBroker` / `NewInProcBrokerE` | In-process message broker over a pubsub.Broker |
| `TaskQueue` / `NewTaskQueue` | Persistent-backed task queue with lease and retry |
| `Worker` / `NewWorker` | Queue consumer with concurrency, ack, and DLQ support |
| `KVPersistence` / `NewKVPersistence` | kvengine-backed queue persistence |
| `Task` | Task value type with ID, payload, and metadata |
| `TaskHandler` | `func(ctx, Task) error` handler contract |

---

## Boundary rules

- Use `NewInProcBrokerE` instead of the deprecated `NewInProcBroker` for explicit error handling.
- Do not use `x/messaging/mq` as a competing family entry point; start new messaging work from `x/messaging`.
- Keep queue adapters local with explicit worker wiring; no hidden auto-registration.
- Protocol support for MQTT and AMQP is not implemented; the interface stubs are compatibility placeholders.

---

## Validation

```bash
go test -race -timeout 60s ./x/messaging/mq/...
go vet ./x/messaging/mq/...
```
