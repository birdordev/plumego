# x/messaging/scheduler — Task Scheduling

> **Import path:** `github.com/birdordev/plumego/x/messaging/scheduler` — sub-package of [`x/messaging`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

**Purpose:** In-process scheduling primitives within the `x/messaging` family. Provides cron parsing, delayed job execution, retry policies (fixed and exponential), in-memory and KV-backed job stores, and an admin HTTP handler.

**Status:** Experimental — API may change. Start app-facing messaging feature discovery from [`x/messaging`](../README.md) before opening this package directly.

---

## First files to read

- `x/messaging/scheduler/module.yaml`
- `x/messaging/scheduler/scheduler.go` — `Scheduler`, `New`
- `x/messaging/scheduler/cron.go` — `ParseCronSpec`, `ParseCronSpecWithLocation`
- `x/messaging/module.yaml`

---

## Key types

| Type / Constructor | Description |
|---|---|
| `Scheduler` / `New` | Core scheduler managing job lifecycle and dispatch |
| `MemoryStore` / `NewMemoryStore` | In-memory job store for local dev and tests |
| `KVStore` / `NewKVStore` | Persistent KV-backed job store |
| `AdminHandler` / `NewAdminHandler` | HTTP handler for scheduler management endpoints |
| `RetryFixed` | Fixed-interval retry policy |
| `RetryExponential` | Exponential backoff retry policy |
| `ParseCronSpec` | Parse a 5-field cron expression |
| `JobID` | Job identity type |
| `TaskFunc` | `func(ctx) error` job handler contract |

Cron evaluates calendar fields in the supplied time zone. A local time skipped
by an offset change has no occurrence. A repeated time can produce two
occurrences at distinct UTC instants.

Changing a scheduled time updates the current heap entry. Cancellation
removes heap entries immediately, including manual triggers. The heap
releases removed references and compacts unused capacity after large
reductions. Terminal job records remain available to status queries and
can be removed with `PruneTerminalJobs`.

---

## Boundary rules

- Keep scheduling behavior stateless between job runs; job state lives in the store.
- Do not use `x/messaging/scheduler` as a competing family entry point; start new messaging work from `x/messaging`.
- Keep retry policy caller-owned; do not hard-code retry behavior in the scheduler.
- The `AdminHandler` mounts protected endpoints; the caller must add appropriate auth middleware.
- HTTP job and dead-letter endpoints emit fixed failure codes. The scheduler
  logs those codes, and the in-process APIs still expose the cause for
  application-owned diagnosis.

---

## Validation

```bash
go test -race -timeout 60s ./x/messaging/scheduler/...
go vet ./x/messaging/scheduler/...
```
