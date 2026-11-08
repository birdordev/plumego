# x/messaging/pubsub — Publish-Subscribe

> **Import path:** `github.com/birdordev/plumego/x/messaging/pubsub` — sub-package of [`x/messaging`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

**Purpose:** In-process publish-subscribe primitives within the `x/messaging` family. Provides a topic-based broker, persistent pub/sub with replay, distributed fan-out, backpressure control, and a zero-dependency Prometheus text exporter.

**Status:** Experimental — API may change. Start app-facing messaging feature discovery from [`x/messaging`](../README.md) before opening this package directly.

---

## First files to read

- `x/messaging/pubsub/module.yaml`
- `x/messaging/pubsub/broker.go` — `Broker` interface and `InProcBroker`
- `x/messaging/pubsub/pubsub.go` — `New`, `Message`, `SubOptions`
- `x/messaging/module.yaml`

---

## Key types

| Type / Constructor | Description |
|---|---|
| `Broker` | Interface: `Publish`, `Subscribe`, `Unsubscribe`, `Close` |
| `InProcBroker` / `New` | In-process fan-out broker |
| `PersistentPubSub` / `NewPersistent` | Persistent broker with replay support |
| `DistributedPubSub` / `NewDistributed` | Multi-node fan-out over a shared backend |
| `ReplayStore` / `NewReplayStore` | Message history for late subscribers |
| `BackpressureController` / `NewBackpressureController` | Slow-consumer protection |
| `PrometheusExporter` / `NewPrometheusExporter` | Zero-dependency Prometheus text-format metrics |

---

## Boundary rules

- Keep the `Broker` interface injected, not package-global; no hidden default broker.
- Do not use `x/messaging/pubsub` as a competing family entry point; start new messaging work from `x/messaging`.
- The `DistributedPubSub` backend is caller-wired; keep distributed topology outside this package.

---

## Validation

```bash
go test -race -timeout 60s ./x/messaging/pubsub/...
go vet ./x/messaging/pubsub/...
```
