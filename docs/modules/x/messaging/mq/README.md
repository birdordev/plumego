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

## Lease ownership

Use `TaskQueue.ReserveFenced` for competing consumers. It produces a fresh
lease owner for each reservation. Pass the returned task's `LeaseOwner` to
ACK, release, renew, and dead-letter operations. The built-in Worker follows
that contract. Bound business side effects with application-level idempotency
so a handler can be retried after a crash or an uncertain downstream result.

The Worker discards expired reservations before starting a handler. An active
handler receives cancellation when its lease can no longer be established.
Panic recovery reports a generic task failure to the retry policy. Task
handlers must honor cancellation and storage methods must honor their contexts.

For PostgreSQL deployment, apply the [queue schema](../../../../MQ_TASK_QUEUE_SCHEMA.md).
With `SQLConfig.DeferredExpiration`, run a bounded `SQLStore.ExpireBatch` for
each of the queued and leased partitions. PostgreSQL claims continue to filter
expired tasks and use task ID to break priority and creation-time ties.
`WorkerConfig.MaxIdlePollInterval` enables bounded exponential backoff with
jitter. The PostgreSQL store supplies a time hint for delayed visibility and
lease recovery. Give each worker its own `Wake` subscription for cross-process
notifications. Periodic polling still runs if a notification is missed.

The Worker retries ACK, release, and dead-letter writes once within their
original finalization budget. A rejected or unresolved write can be observed
through the optional `FinalizationErrorHook`. Persisted retry and dead-letter
reasons use fixed messages. A configured deduper checks for committed work
before the attempt limit so lost ACKs can be recovered without running a
completed handler again.

Before calling a handler, the Worker confirms a timed lease with its current
claim token and refreshes the lease window. The confirmation is bounded by
the prior expiry and renewal interval. A stale or unconfirmed claim does not
enter the handler.

`WorkerConfig.MaxQueueWait` starts at the current attempt's availability
time. `TaskTimeout` bounds a single handler and its reconciliation call.
`MaxTaskAge` is a total budget starting at original enqueue time, including
scheduled waiting. These limits are opt-in. The Worker distinguishes queue
wait and total age expiry from an execution timeout that can still be retried.
Stop halts claims before draining. At the drain deadline, it cancels execution
and releases claims that are safe to hand off. A handler or outcome check
still running retains its lease until expiry. Protect side effects with the
claim token and a business idempotency key.

The optional `WorkerConfig.BeginExecution` callback can persist a fenced
execution start and return its sequence number. With this callback, the
Worker uses the execution sequence for the retry budget and backoff. Handlers
receive the claim sequence in `Task.Attempts` and the execution sequence in
`Task.ExecutionAttempts`.
PostgreSQL `SQLConfig.BlockedClaimsTable` can point to a trusted table or view
of blocked task IDs. Claims and future availability hints exclude these tasks.
Set `SQLConfig.PriorityAging` to a positive duration to promote eligible
PostgreSQL tasks after that wait. Effective eligibility starts at the latest
of creation, availability, and any expired lease. Aged tasks are selected
by earliest eligibility, with priority and creation time breaking ties.
Zero preserves strict priority.
A `BeginExecution` callback can return `ErrClaimPaused` to release an already
prefetched claim back to the queue without running its handler.
A callback that has already committed a fenced state transition for the current
claim can return `ErrExecutionHandled`. The Worker releases its local capacity
and leaves the committed state to the queue.

Handlers can return a `TaskFailure` for permanent, rate-limited, or uncertain
outcomes. Permanent errors go straight to dead letter. Rate-limited tasks
wait at least `RetryAfter`. An uncertain outcome is isolated unless
`ResolveOutcome` confirms either that work was committed or that it is safe
to retry. Only fixed reason strings are persisted.

`Worker.Identity()` contains the logical consumer name, unique instance ID,
and optional pool and version labels. Claims include the instance ID and a
separate per-reservation token.

`SQLStore.InsertBatchTx` inserts up to 128 prepared tasks in a caller-owned
transaction. PostgreSQL `SQLStore.AckBatch` checks each task's lease token and
returns its result in input order. A stale claim affects only its own result.
A database or commit error provides no per-item confirmation.
