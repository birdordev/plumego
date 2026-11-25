# PostgreSQL task scheduling

This reference uses explicit API, scheduler, and Worker roles sharing a
PostgreSQL database. The pgx driver is isolated to this standalone module.
The sample task records a durable completion receipt.

## Start from a new database

Set `SCHEDULER_DATABASE_URL` to a PostgreSQL DSN. Set
`SCHEDULER_API_TOKEN` and `SCHEDULER_TENANT` for the API role, then run from
the repository root.

```sh
go -C reference/with-scheduler run . -role migrate
go -C reference/with-scheduler run . -role api
go -C reference/with-scheduler run . -role scheduler
go -C reference/with-scheduler run . -role worker
go -C reference/with-scheduler run . -role finalizer
go -C reference/with-scheduler run . -role expiry
go -C reference/with-scheduler run . -role outbox
```

Run the service roles in separate terminals. The API binds to
`127.0.0.1:8094` by default. The migration command accepts
`-queue-migration` for deployments with a different working directory.
Schedule and Worker roles can each run with multiple instances.
The expiry role clears task TTLs in batches of up to 128 queued tasks and
128 tasks with expired leases. Workers always exclude out-of-TTL tasks from
claims. Run the expiry role to materialize the status and release tenant quota.
See the [queue capacity probe](queue-capacity.md) for local measurements and
reproducible lock and query-plan checks.
Workers increase empty-poll intervals from 200 ms up to two seconds and use
the earliest future availability to shorten waits. Each worker uses one
dedicated PostgreSQL session for queue notifications. Use a direct or
session-pooled connection for this session. Notification delivery follows
commit and includes no task data. Workers continue polling during disconnects.

Set `SCHEDULER_WORKER_PROBE_ADDR`, for example `127.0.0.1:8095`, to expose
the Worker process's `GET /readyz` locally. On SIGTERM, readiness is withdrawn
and claiming stops before accepted work drains. The drain deadline defaults to
10 seconds and can be set with `SCHEDULER_WORKER_DRAIN_TIMEOUT`. The Worker
requests cancellation near the end of that budget and only releases claims that are safe
to hand off. Claims that are still active can be recovered after expiry.
`SCHEDULER_WORKER_LEASE_DURATION` defaults to 30 seconds.

### Recovery readiness

The API exposes `GET /readyz`. The Worker exposes the same contract on its
optional probe listener. The scheduler and finalizer expose it on their
optional `SCHEDULER_METRICS_ADDR` listeners. Each probe allows one second for
storage and schema validation and for inspecting active recovery state.

A pending, consistent finalization remains recoverable. The finalizer retries
it, including after a process restarts. Readiness returns 503 when an intent is
still pending 30 seconds after it was first recorded, the intent no longer
agrees with the current queue claim, required intent fields are missing, or
a nonexpired active queue entry has no topic in the task contract registry.
The same response status covers storage unavailability and schema mismatch.
These responses contain fixed reasons without task identifiers or payloads.
Process liveness remains separate and the finalizer continues processing
recoverable records while readiness reports a fault.

To pause mutation admission when synchronous replication is unavailable,
set `SCHEDULER_SYNC_STANDBY` to the designated standby application name.
The API checks for a streaming synchronous standby, `remote_apply`, and
the matching single-standby configuration before reporting ready or
accepting a mutation. Grant the API database role access to the replication
status fields it reads. Keep synchronous commit enforced for application
connections and isolate old primaries before promotion. Status reads remain
available on an active writer while replication is restored.

Use restricted database access to identify the affected runs in `mq_tasks`
and `mq_task_attempts`. Review the original intent and current lease before
repairing an inconsistent record. Workers permanently reject malformed
payloads on registered topics and retain the failure in dead-letter history.
During a version rollout, a topic recognized by the registry can be waiting
for a compatible Worker. Verify that the planned pool and version are
deployed before enabling submissions for that version.

### Task versions

For `demo.record`, the default `task_version` is 1. Version 1 accepts a JSON
value. Version 2 requires an object with a nonblank `message` string of at most
4,096 bytes. Unknown types and versions are rejected before enqueue. A payload
is limited to 64 KiB. Optional `metadata` accepts up to eight labels, each with
a key of at most 64 bytes and a value of at most 256 bytes. Optional
`resource_refs` accepts up to eight opaque identifiers of at most 128 bytes
each. Keys and references use ASCII letters, digits, underscores, and hyphens. The
combined serialized options are limited to 4 KiB, and references are stored as
identifiers for the business handler. Submission keys include the task
contract in the fingerprint. Explicit version 1 and omitted version share the
legacy fingerprint. Schedules retain the options for each materialized run.

Schema version 16 gives version 2 its own queue topic. Set
`SCHEDULER_WORKER_TASK_VERSIONS` to `1`, `2`, or `1,2` (default). A v1-only
Worker continues claiming existing work while a v2-capable Worker starts.
Enable version 2 submissions once a compatible Worker is running. An item
that has the wrong stored contract for its topic is dead-lettered before the
business handler runs. Apply the schema migration and use v16 Worker binaries
for the mixed-capability deployment.

## HTTP contract

- `POST /runs` accepts a Bearer token, an `Idempotency-Key` header, and JSON
  such as `{"topic":"demo.record","payload":{}}`. Set `run_at` to an RFC 3339
  timestamp for delayed delivery. The API returns `202` with a status URI
  only after committing the run and its queue entry. If a submission returns
  `503` or the response is lost, retry with the same key and request content.
  A committed submission returns the original status URI. Reusing the key with
  different content returns `409`.
- `POST /runs/batch` accepts `{"runs":[{"idempotency_key":"request-1","topic":"demo.record","payload":{}}]}`.
  Send up to 128 items with distinct keys within the 128 KiB request limit. Each key uses the
  same tenant scope and fingerprint as `POST /runs`. The response returns
  `202` and the run IDs in input order. Validation, fingerprint conflict,
  quota rejection, or a write failure rolls back all new items. On an
  uncertain response, resend the same items with their original keys.
- `GET /runs/:id` reports the queue state and whether a completion receipt
  exists. Bearer authentication selects the configured tenant. The tenant cannot
  be selected through the request body.
  Run status and list results include `claim_attempts` and `execution_attempts`.
  Queued results expose `available_at`, including the persisted next retry time.
- `GET /runs/:id/result` downloads the tenant's committed JSON receipt with the
  run ID and completion time. Missing, pending, and other tenants' results
  return `404`. The response uses `Cache-Control: private, no-store`.
- `GET /schedules/:id/occurrence?scheduled_at=<RFC3339>` looks up the planned
  occurrence at the supplied instant, with precision up to one microsecond.
  The response contains the run ID and the version of its schedule.
- `GET /runs/:id/attempts` returns up to 50 claim attempts in descending order.
  Pass `next_before` as `before` to read older attempts. Completion times and
  safe failure codes appear once an attempt finishes. Since schema version 10,
  the attempt and its queue transition commit together. Existing history is
  preserved across the upgrade. If an older best-effort write was skipped, its
  attempt number remains a gap in the history. For executions registered by
  schema version 14 or later, history also includes `execution_no` and
  `execution_started_at`.
- `GET /runs` lists tenant runs by descending creation time and ID. Use
  `limit` (default 50, maximum 100), an optional `status` filter, and the
  returned `next_cursor` for the next page. Keep the same status filter while
  paging. Inserts newer than the cursor appear when a caller starts a new list.
  Status is read at request time and may change between pages. Set
  `include_total=true` to request a separate exact count for the current
  status filter, before the cursor.
- `POST /runs/:id/cancel` returns `204` after canceling a queued or leased
  run and revoking its lease. Repeated cancellation returns `204`. A run with
  a committed result or another terminal state returns `409`.
- `POST /schedules` accepts JSON such as
  `{"id":"hourly","expression":"@hourly","time_zone":"UTC","topic":"demo.record","payload":{}}`.
  The scheduler commits each occurrence, queue entry, and next trigger together.
  The default `schedule_mode: fixed_rate` advances from the persisted planned
  time and processes overdue occurrences in bounded batches. Set `misfire_policy` to `skip`,
  `coalesce`, or `catch_up` (default). The two-second grace treats recent
  occurrences as on time. Skip advances beyond now; coalesce emits one
  retained missed occurrence; catch-up emits individual occurrences. The
  `catch_up_window_seconds` setting limits how far back runs may be generated
  (default 3600, range 1–86400). Status reports the latest nontrivial decision.
  Set `schedule_mode: fixed_delay` for an `@every` interval from one microsecond
  through 365 days. The next delay starts when the preceding automatic run
  first reaches a terminal queue state. Retries and manual runs leave that
  clock untouched. The schedule status includes `waiting_run_id` and a null
  `next_fire_at` while waiting. After downtime, a due fixed-delay schedule can
  catch up with one run within the configured window. If skipped, it starts
  a new delay from the scheduler's current time.
- `PATCH /schedules/:id` accepts `version` and one or more pause instructions.
  `paused` controls future materialization. The schedule's misfire policy
  determines catch-up after it resumes. `claim_paused` controls claims of
  queued runs, including manual triggers. A prefetched claim that reaches its
  execution check during the pause returns to the queue, without spending an
  execution attempt. Setting `claim_paused` to false wakes Workers to claim the
  backlog. `cancel_running: true` cancels the schedule's currently leased
  runs with no committed result and returns the number canceled. It is a
  one-time instruction. Combine it with `claim_paused: true` to block further
  claims. Omitted switches retain their values. Use the returned version for
  the next update. A stale version returns `409`.
- `PUT /schedules/:id` replaces the schedule definition using its expected
  `version`. A cadence or timezone change resets the cursor from the update;
  payload and policy changes preserve it. Already generated runs keep their
  original payload and version.
- `POST /schedules/:id/trigger` accepts `Idempotency-Key` and returns `202`
  with an independent run URI. It can trigger a paused schedule explicitly.
  Retrying the same command returns the original run even after a definition
  change. Manual triggers leave the periodic cursor and last-fired status
  unchanged.
- `GET /schedules/:id` reports both pause switches, the effective version,
  the persisted cursor, the most recent occurrence, the IANA name,
  `time_zone_rules_sha256`, the configured `jitter_window_ms`, and the next
  eligible dispatch time in `next_dispatch_at`.
- `GET /healthz` and `GET /readyz` expose basic API liveness and schema readiness.

Worker result writes check the live queue claim and tenant in the same
transaction. The run ID is the business idempotency key for the completion
receipt. The queue can redeliver after a crash between result commit and ACK.
On redelivery the Worker checks the receipt before enforcing the attempt limit.
Version 14 charges the execution budget when the Worker commits a start
marker for its current lease, immediately before invoking the handler. Claims
that expire before a start marker do not consume the budget. A crash after the
start marker commits can consume an attempt even if the handler has yet to run.
Failed executions persist the next visibility time in the queue.
Migration initializes the execution counter from the legacy claim counter
to preserve the previous retry budget. When upgrading to version 14, drain
the old Workers before applying the migration and resume with the new Worker
binary so all new executions register a start. Apply the same Worker upgrade
sequence for version 15 before using claim pauses.

### Execution overlap

Schema version 17 supports an optional `overlap` object on direct, batch, and
scheduled submissions. Drain older Workers before migrating and enable the
policy after all Workers use the new binary.

| Example | Behavior |
| --- | --- |
| `{"policy":"allow"}` | No execution slot is required. Omitting the object has the same effect. |
| `{"policy":"skip","scope":"key","key":"customer-42"}` | An active holder for this business key in the same tenant causes a terminal `skipped` run. |
| `{"policy":"serial","scope":"tenant"}` | A run waits while another run participating in the same tenant scope holds the slot. |
| `{"policy":"serial","scope":"schedule"}` | Automatic and manual runs of the same schedule share its slot. Only schedule definitions accept this scope. |

Only runs with the same tenant, scope, and key compete for a slot. Instances
retain the options captured at submission or materialization. The schedule
status returns its configured overlap object. A skip does
not invoke the handler, consume an execution attempt, or create a dead
letter. It still completes an automatic fixed-delay occurrence. A serial
conflict releases the claim and makes the task available again after 500 ms.
It can add a claim attempt and preserves the execution budget. Serial
admission follows queue availability and does not guarantee FIFO ordering.
The attempt API reports `overlap_skipped` or `overlap_deferred`.

Slot ownership follows the current database lease, including renewals and
replacement claims. A released, expired, or completed lease leaves its slot
available for reuse. The slot table retains one row per distinct group.
Handlers must still honor cancellation and guard downstream effects with
business idempotency keys and, where supported, claim fencing.

The Worker groups overlapping ACKs for up to two milliseconds. Each item still
checks its own claim token. A stale claim returns a lease error for that item.
A database write error rolls back the group, and unresolved confirmations
follow the existing retry path.
Set `SCHEDULER_WORKER_ACK_WINDOW` (0–50 ms) and
`SCHEDULER_WORKER_ACK_BATCH` (1–128, default 16) to compare confirmation
settings with the same workload, durability policy, and WAL measurements.

### Finalization recovery

Deploy at least one `finalizer` process alongside the Workers after applying
schema version 18. Drain older processes before this migration. Workers record
the intended ACK, retry release, or dead-letter transition against the current
claim before attempting the queue write. Attempt history exposes
`execution_outcome` separately from `finalization_operation` and
`finalization_state`. A write error can add `finalization_failure` with
`retryable`, `lease_lost`, or `commit_unknown`.

Pending write intents are protected from claim takeover and expiry cleanup,
including after the original lease expires. After a short grace period a
finalizer retries the recorded transition under a lock on the original queue
row. Replicas skip work locked by another process. The finalizer applies the
original retry timestamp, and commits dead-letter insertion and queue state
together. A committed transition marks the intent `confirmed` in the same
transaction, so losing the write response does not cause a second transition.
A competing cancellation that changes the queue state first marks the intent
`superseded`. If PostgreSQL cannot record the intent at all, recovery falls
back to lease expiry, result deduplication, and the task's business idempotency
contract. The journal adds a database write before each queue transition.

### Latency metrics

For each scheduler, Worker, or finalizer replica, set `SCHEDULER_METRICS_ADDR`
to an internal listening address, for example `127.0.0.1:8096`. Scrape
`GET /metrics` from that address. Use a distinct port when roles share a host.
Schema version 19 adds the timestamp used to measure finalization across
processes. Deploy matching binaries and schema. A pending v18 intent can
still be reconciled, but it has no finalization latency sample.

The latency metric families are Prometheus cumulative histograms in seconds.
They use the physical queue, logical task type, and a fixed stage result
as labels.

| Metric | Timing and result |
| --- | --- |
| `plumego_scheduler_trigger_delay_seconds` | Planned occurrence to successful materialization commit, with result `created`. |
| `plumego_scheduler_dispatch_delay_seconds` | Eligible dispatch time to successful materialization commit, with result `created`. |
| `plumego_scheduler_queue_delay_seconds` | Current availability time to handler start, with result `started`. It excludes time spent waiting for a scheduled retry to become eligible. |
| `plumego_scheduler_execution_duration_seconds` | Handler start to return or panic, labeled with the handler outcome. |
| `plumego_scheduler_finalization_delay_seconds` | First durable confirmation intent to an acknowledged queue transition, labeled `ack`, `release`, or `dlq`. Recovery time is included in the finalizer replica's sample. |

Keep host and database clocks synchronized for wall-clock measurements
across processes. Small negative samples are clamped to zero. The histograms
are process-local and reset at restart. A process exit or lost commit reply
can omit a sample. Use run IDs in attempt history and failure logs to
investigate individual runs.

### Service objectives and alerts

Load [prometheus-rules.yml](prometheus-rules.yml) into Prometheus. These
reference rules assume one deployment per rule group. Scrape each role's
internal metrics endpoints at 15-second intervals with jobs named
`plumego-scheduler`, `plumego-worker`, and `plumego-finalizer`. Apply the
v20 migration before starting the matching binaries. It adds an index for
recent dead-letter lookups.

| Signal | Reference objective and alert |
| --- | --- |
| Scheduling delay | At least 99% of materialized occurrences commit within five seconds of becoming eligible for dispatch over ten minutes. An enabled dispatch over five seconds overdue also counts as a breach, even if no commits are observed. Alert after two minutes. |
| Runnable backlog | The oldest executable task has been eligible for at most 60 seconds. Alert after a sustained two-minute breach. |
| Lease confirmation and renewal | Zero failed lease writes over five minutes. Alert after one minute. This includes the ownership confirmation before handler entry. |
| Queue finalization | Zero failed ACK, Release, and DLQ writes over five minutes, including attempts made by the finalizer. Alert after one minute. Expected stale ownership and shutdown cancellation are excluded. |
| Dead-letter growth | At most ten new persisted dead-letter entries in a rolling five-minute window. Alert after one minute. |
| Telemetry availability | A failed or missing scheduler storage probe for 30 seconds is critical. A process role without any reachable scraper target for one minute is critical. |

The scheduler reads shared gauges with a one-second database deadline.
Runnable age includes queued tasks and expired leases eligible for takeover,
excluding delayed, expired, paused, and finalization-pending work. The
five-minute dead-letter count saturates at 10,000 entries. Shared readings use replica minima or
maxima in the rules, and process-local counters use sums of increases.
A failed database read emits `plumego_scheduler_storage_probe_success 0`
and omits the three shared workload gauges. A successful idle scrape reports
zero for those gauges.

For scheduling and backlog alerts, check the oldest due occurrence and
whether Worker capacity is saturated. For lease, finalization, and storage
alerts, check PostgreSQL availability and lock waits, and inspect attempts
for pending confirmation. For dead-letter growth, inspect failure codes
before resuming failed work.

Validate the rule transitions before loading them:

```sh
promtool check rules reference/with-scheduler/prometheus-rules.yml
promtool test rules reference/with-scheduler/prometheus-rules.test.yml
```

### Async trace correlation

Schema version 21 adds source correlation for schedule definitions. An
authenticated command starts a server trace and assigns a server request ID.
Single and batch submissions, manual triggers, and periodic occurrences
carry only the request, trace, and span IDs from their source. Periodic
occurrences use the create or replacement command for the schedule
definition used at materialization. Idempotent retries preserve the first
committed origin. Dead-letter replay preserves that origin and records the
replay command as a second link. Existing tasks without source metadata
still execute.

Workers start a fresh consumer span on each handler invocation, linked to the
durable origin and replay command when available. Worker execution contexts
inherit cancellation from the task runtime. Set
`SCHEDULER_WORKER_TASK_TIMEOUT` to override the one-minute per-attempt
deadline.

Set `SCHEDULER_TRACE_LOG=1` on API and Worker replicas to emit completed
reference spans as JSON in the process log. Look up the command by its
response request ID, or look up a worker span by run ID and execution
attempt. The reference exports through its process log.

### Time zone rules and rollout

Calendar Cron expressions use the schedule's IANA time zone. A local time
in a daylight-saving gap has no occurrence. When a local time appears
twice, both UTC instants are eligible, subject to the configured misfire
policy. Day-of-month schedules skip months without the specified day. The
29th of February only occurs in leap years. `@every` and fixed-delay
intervals use elapsed time.

Schema v22 stores the TZif rules with each named zone. Materialization reads
those rules and the UTC cursor from the same row. Deployments can set
`SCHEDULER_TZDATA_ZIP` to a trusted archive containing TZif entries named by
IANA zone. The default resolver checks `ZONEINFO`, then
`/usr/share/zoneinfo`, then the running Go installation's
`lib/time/zoneinfo.zip`. UTC has built-in rules.

Drain the pre-v22 binaries before migrating. Run the migration with the
zone archive used by the deployment being upgraded. Migration pins one
snapshot per existing zone into the affected schedules while retaining
their UTC cursors. Every requested zone must exist in that archive for the
migration to commit.

After v22, scheduler replicas can roll to a new archive while existing
plans continue with their stored rules. An ordinary definition replacement
in the same zone preserves the snapshot. To adopt the newer rules without
changing the cadence, send the full definition and its current `version`
with `"refresh_time_zone":true` in the replacement request. The pending UTC
cursor remains the next trigger subject to the existing misfire policy.
Later occurrences use the replacement rules. New definitions and changes
to another zone take their snapshot from the API replica's configured
archive.

### Versioned business calendars

Authorize calendar publication by configuring `SCHEDULER_CALENDAR_PUBLISHER_ACTOR`
to match the publishing API instance's `SCHEDULER_API_ACTOR`. The setting
defaults to no publisher. Use the authenticated
`PUT /business-calendars/:name/versions/:version` endpoint with a positive
integer version. Calendar names and versions are scoped to the API tenant.

```json
{
  "time_zone": "Asia/Shanghai",
  "open_weekdays": ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday"],
  "exceptions": [
    {"date": "2024-12-31", "open": false},
    {"date": "2025-01-01", "open": false},
    {"date": "2025-01-05", "open": true}
  ]
}
```

Use full weekday names, at least one weekday, and up to 366 unique civil-date
exceptions. Exceptions override the weekday rule. Publication pins the
calendar's zone rules. Repeating the same publication returns 204. Changing
the rules at the same name and version returns 409. A successful initial
publication records a `calendar.publish` audit entry.

Add `"calendar":{"name":"settlement","version":1}` to a wall-clock Cron
definition sent to `POST /schedules` or `PUT /schedules/:id`. The Cron
expression produces candidate instants in its own time zone. The calendar
filters them by civil date in its pinned time zone. Choose a daily Cron for
a daily task that must run on makeup weekends. An exception can open a day,
and that day still needs a Cron occurrence. Interval expressions and
completion-based fixed delays cannot bind a business calendar. Explicit
manual triggers remain available outside calendar hours.

Publishing a new calendar version leaves schedules pinned to their existing
version. Change a binding with a version-checked schedule replacement.
Changing or removing a binding resets the cursor to the first matching
occurrence after a database timestamp captured under the schedule lock. Old
unmaterialized times are discarded at this boundary. Already materialized runs retain their
schedule and calendar versions, including while a Worker retries them.
Include the calendar pin in a full replacement to retain it. Omit it to remove
the calendar. Pause and resume preserve the calendar. Misfire policies apply
to eligible occurrences. A calendar with no further eligible occurrence
clears the automatic cursor. A definition must have an eligible occurrence
when creating the schedule or rebinding the calendar.

`GET /schedules/:id/calendar-preview?after=2024-12-29T16:30:00Z&limit=3`
returns occurrences strictly after the timestamp, up to the requested limit
(default 20, maximum 100). Each entry gives the UTC scheduled and dispatch
times plus the calendar's civil date. The response includes the schedule and
calendar versions used for the forecast. It evaluates the current rules for
the requested interval, including historical dates. Pause, quotas, missed
occurrence policies, and the live cursor can change which forecast entries
actually produce runs. Read the bound calendar under
`GET /schedules/:id` and the original calendar for a materialized occurrence
under `GET /schedules/:id/occurrence?scheduled_at=...`.

### Scheduler shard ownership

Schema v23 assigns each tenant and schedule ID to a stable shard out of 64.
It indexes the due cursor and fixed-delay completion records by shard.
Drain old binaries before migrating. The migration computes stored shard
keys for existing schedule and run rows and builds the supporting indexes.

Each scheduler process has a unique owner ID, renews its membership every
500 ms, and uses the shared PostgreSQL clock for the three-second lease.
Active IDs are ranked by a stable hash for each shard. A membership change
reassigns only the shards whose highest-ranked active owner changes.
On clean shutdown a scheduler removes its membership. After an abrupt
exit, a surviving scheduler transfers the shards once the old lease expires.

Each replica queries due work only in its own shards. It checks ownership
again inside every materialization transaction and holds that assignment
until commit. Shard transfers skip locked assignments. Candidate reads skip
individual locked schedules so other work can proceed. Each polling cycle
tries at most 64 operations, serving each due shard once per round. A
transfer resumes from the committed UTC cursor using the schedule's
existing misfire policy.

Inspect assignments with `scheduler_shards` and lease deadlines with
`scheduler_shard_members`. The three-second lease plus the next heartbeat
is the nominal failure detection window. Database unavailability or a
contended assignment delays transfer until the relevant operation can
complete.

### Regional shard ownership and failback

Regional scheduling uses one writable PostgreSQL authority. Both regions use
that authority and retain the same tenant, schedule, run, and idempotency
identities. Deploy scheduler and Outbox processes with `SCHEDULER_REGION`.
The default region is `local`, which is also the initial home of all 64
shards. A standby region can maintain connections and membership without
materializing or delivering for shards it does not own. API and Worker
processes share the existing durable submission and task-claim contracts.
A topology that also requires residency for Worker execution should place
execution pools and their downstream credentials accordingly.

Before the first transfer, drain scheduler and Outbox processes from releases
older than schema v38 and start v38-compatible copies. Use independent
operator database credentials for transfers. Restrict scheduler membership
and ownership configuration to trusted deployments. Grant runtime roles only
the columns and tables they update. Keep writes to `scheduler_shards.region`,
`scheduler_shards.region_epoch`, and `scheduler_region_transfers` with the
operator.

Check the current primary region and epoch for the affected shard:

```sql
SELECT shard, region, region_epoch, owner_id FROM scheduler_shards ORDER BY shard;
```

For example, after isolating the failed region, transfer shard 7 from
`local` at epoch 1 to `east` using the operator's writable database endpoint:

```sh
SCHEDULER_TRANSFER_SHARD=7 SCHEDULER_TRANSFER_EPOCH=1 \
SCHEDULER_TRANSFER_FROM_REGION=local SCHEDULER_TRANSFER_TO_REGION=east \
SCHEDULER_TRANSFER_ACTOR=incident_operator \
go -C reference/with-scheduler run . -role region-transfer
```

Transfer compares the old region and epoch, then moves ownership, increments
the epoch, and records the operator in the same transaction. The transfer
waits for existing shard transactions within a ten-second command budget.
Retry with the same inputs when the commit result is uncertain. A different
command using an already applied epoch conflicts. Move further shards
individually after confirming capacity in the destination. Failback is another
transfer using the currently active region and epoch. It preserves the
schedule cursor and previously committed runs.

A scheduler with an earlier epoch cannot materialize after reconnection.
Outbox claim and completion check the current shard region, and completion
also checks its claim epoch and lease. A scheduled run uses its schedule
shard. A direct run uses a stable hash of tenant and run ID. If a receiver
committed before a region lost connectivity, the replacement sender can
retry after lease expiry using the same event ID. The receiver must deduplicate
by that ID across regions. Compare delivery attempts and committed effects
separately.

If the writable database itself also moves, fence the old primary and
validate the replication position before promotion, as in the
[PostgreSQL failover drill](#postgresql-failover-drill). Regional shard epochs
are enforced against a shared writable authority. Production deployment
must maintain that authority across network partitions and configure its
data-loss and recovery objectives separately.

### Dispersing simultaneous runs

Schema v24 adds the optional integer `jitter_window_ms` to schedule
creation and replacement. Use zero, the default, for exact-time dispatch.
For plans that can start later, choose an allowed window up to 60,000 ms.
Drain old processes before applying v24. Start matching binaries and load
the updated dispatch-delay alert rules after migration.

The scheduler derives a stable nonnegative offset from the tenant and plan
ID. The offset leaves up to 400 ms of the configured window, capped at one
quarter of that window, for the polling and commit path. A tenant's plans
are still distributed across the existing shards.

The original UTC occurrence and the fixed-rate cadence remain the run's
identity. The queue becomes eligible at `next_dispatch_at`, and automatic
materialization considers only eligible plans in bounded batches. Misfire
age and dispatch SLO start at eligibility. Manual triggers dispatch
immediately. Replacing the window applies to the pending schedule cursor
and future automatic runs. Already materialized work keeps its timestamps.

The window bounds the intentional offset. Database outages, quota rejection
or an overloaded scheduler can delay the commit further. Observe the
dispatch-delay histogram to measure that additional lag.

### Priority and aging

Single and batch submissions and schedule definitions accept
`"priority":"low"`, `"normal"`, or `"high"`. Normal is the default. For
direct requests, explicit normal and omitted priority use the same
idempotency fingerprint. Automatic and manual runs take their queue
priority from the schedule version used to generate them. Roll schedulers
forward before enabling custom priority on recurring plans.

PostgreSQL Workers use a 30-second aging threshold by default. Set
`SCHEDULER_WORKER_PRIORITY_AGING` to a Go duration or `0` for strict
priority. Configure one threshold across Worker replicas for the same queue.
Before aging, higher priority claims first. Once aged, tasks are claimed
by earliest effective eligibility, then by priority, creation time and ID.
Eligibility cannot predate creation and restarts at the end of an expired
lease or a scheduled retry. This also prevents a new task with a backdated
`run_at` from jumping ahead of older aged work.

For a continuously eligible task with `N` earlier or tied claims ahead
of it, the wait to claim is bounded by the aging threshold plus
`(N+1) × T` when the queue can make at least one eligible claim per
`T` and no older eligibility is introduced. Capacity, locks, paused
work resuming, and downstream execution still affect the observed wait.

### Shared execution budgets

Schema v25 adds cluster-wide admission budgets. Drain older Workers before
migrating, then configure rules in PostgreSQL. Each rule can limit concurrent
executions, impose a minimum interval between admissions, or do both. A zero
setting leaves that dimension unlimited.

```sql
INSERT INTO scheduler_execution_budgets
    (scope, budget_key, max_concurrent, min_start_interval_ms)
VALUES ('tenant', 'example-tenant', 4, 100),
       ('type', 'demo.record', 12, 0),
       ('downstream', 'result-db', 8, 200);
```

The current task registry maps both versions of `demo.record` to that
logical type and its trusted `result-db` dependency. A request cannot
select a dependency budget. All matching rules apply together at execution
registration. When any rule is saturated, the claim returns to the queue
with `budget_deferred` and leaves the execution attempt available.

Slots belong to a queue claim. Acknowledgment, release, cancellation,
expiration or a replacement claim clears its slots. After a worker crash,
new admission reclaims an elapsed lease once PostgreSQL can lock its queue
row. A late heartbeat cannot revive an elapsed lease. Business handlers
still need to honor cancellation and fence external side effects.

Rate rules pace registered admissions using the PostgreSQL clock. Actual
downstream call timings also depend on handler and transaction latency.
Drain affected work before enabling a new concurrency rule on a running
scope so every active invocation has a registered slot. When tightening
an existing rule, outstanding slots count until their claims end.

### Independent Worker pools

Run one or more Worker processes per pool. The selected pool determines
which topics that process claims. Deploy API, scheduler, and the
corresponding Workers before submitting tasks for a new task type.

| Pool | Task type | Default concurrency | Default PostgreSQL open / idle connections |
| --- | --- | --- | --- |
| `short` (default) | `demo.record` v1 and v2 | 4 | 16 / 8 |
| `cpu` | `demo.compute` v1, a bounded checksum workload | Up to 4, based on `GOMAXPROCS` | 8 / 4 |
| `io` | `demo.io` v1, simulated latency on a PostgreSQL connection | 2 | 8 / 4 |

The I/O example accepts `{"delay_ms":2000}` and allows 1–30,000 ms.
Set `SCHEDULER_IO_ENDPOINT` and `SCHEDULER_IO_TOKEN` on the I/O pool to
measure an authenticated downstream HTTP call. The downstream receives a POST
with the delay in milliseconds as plain text and returns 204 when finished.
Use a side-effect-free delay endpoint. The client uses a configured URL
and rejects redirects. Without the setting, the reference uses PostgreSQL delay.
Choose deployment resources and override concurrency and database
connections per process with `SCHEDULER_WORKER_CONCURRENCY`,
`SCHEDULER_WORKER_DB_MAX_OPEN`, and `SCHEDULER_WORKER_DB_MAX_IDLE`.
Idle connections default to the smaller of the profile default and the
open limit. Leave capacity for lease renewals and finalization when
choosing a database limit. Each Worker uses one additional PostgreSQL
session for queue notifications.

Set `SCHEDULER_WORKER_PROTECTED_TENANTS` on a shared Worker pool to
give selected tenant IDs explicit claim turns. Specify up to 32 comma-separated
IDs. A protected turn claims one task and returns to shared claiming when
the tenant has no eligible work. Tenant filtering uses the same lease,
priority aging and blocked-claim checks.

```sh
SCHEDULER_WORKER_POOL=short SCHEDULER_METRICS_ADDR=127.0.0.1:8096 go -C reference/with-scheduler run . -role worker
SCHEDULER_WORKER_POOL=cpu SCHEDULER_METRICS_ADDR=127.0.0.1:8097 go -C reference/with-scheduler run . -role worker
SCHEDULER_WORKER_POOL=io SCHEDULER_METRICS_ADDR=127.0.0.1:8098 go -C reference/with-scheduler run . -role worker
```

Use `plumego_scheduler_pool_oldest_runnable_seconds` from the scheduler
and the pool-labeled Worker metrics
`plumego_scheduler_worker_active`, `plumego_scheduler_worker_capacity`,
`plumego_scheduler_worker_db_in_use`, `plumego_scheduler_worker_db_max_open`,
and `plumego_scheduler_worker_db_waits_total` to size each deployment.
Execution histograms show throughput and duration by task type. Replicas
have independent Go concurrency and connection pools and still use the
same PostgreSQL server and cluster-wide budgets. Set deployment CPU and
backend limits accordingly.

The Worker writes the receipt and event to the outbox in the same transaction.
The independent outbox role delivers events to a sample database-backed
recipient using the run ID as its deduplication key. Version 2 backfills pending
events for earlier receipts. Stop version 1 processes before applying the upgrade.

## Resumable bulk cancellation

Apply schema v26 and run the independent executor:

```sh
go -C reference/with-scheduler run . -role bulk
```

Send an authenticated `POST /bulk-cancellations` with an `Idempotency-Key`
and a selection such as `{"topic":"demo.record","limit":100}`. The API
persists a preview of up to 500 queued runs for the authenticated tenant and
the exact queue topic. Selection is ordered by availability and then run ID.
The response includes the frozen IDs, filter and count. Repeating the same
key and filter retrieves the original operation. A different filter with
the same key returns a conflict.

Use `PATCH /bulk-cancellations/{id}` with `{"paused":false}` to start or
resume the plan, and `{"paused":true}` to pause it. A pause waits for the
currently committing batch. `GET /bulk-cancellations/{id}` returns total
and processed counts and each item's `pending`, `canceled`, `changed` or
`missing` outcome. Each transaction commits up to 16 cancellations and
their outcomes together. A replacement executor resumes pending items.
An item that has changed since the preview, including one already claimed
by a Worker, is reported and skipped. Runs submitted after the preview are
outside its scope. Cancellation audit rows retain the original plan actor
and request ID.

## History retention

Apply schema v27 and start `go -C reference/with-scheduler run . -role archive`.
The role moves up to 128 rows per group in separate restart-safe transactions.
Retention windows use PostgreSQL time:

| Group | Default in hot tables | Configuration |
| --- | --- | --- |
| Queued and leased tasks | Until terminal | Queue expiry and lease policy |
| Terminal queue rows | 90 days | `SCHEDULER_TERMINAL_RETENTION` |
| Old attempts of terminal runs | 30 days | `SCHEDULER_ATTEMPT_RETENTION` |
| Audit rows | 180 days | `SCHEDULER_AUDIT_RETENTION` |

Set durations in Go duration syntax, for example `2160h`. Pending
finalization and undelivered Outbox records protect their queue rows. A
dead task's attempts stay hot until its queue row is archived.
The usual replay rules apply while the dead task is in the hot queue.
Archival ends replay eligibility and preserves dead-letter history. API status, list,
attempts, result reads and idempotency keys survive archival. Queue archives
retain status and counters. The discarded hot queue payload is unavailable
after the move. Results, Outbox history and the cold tables have separate
lifetimes and are retained by this example.

The scheduler's `plumego_scheduler_archived_runs_total` reads four
pre-aggregated counters maintained by the archive role. Replicas expose the
same database counters. Use a maximum across replicas. Ordinary deletion
allows PostgreSQL to reuse hot table space. Physical shrinkage requires a
table rewrite. The capacity test rewrites only its disposable test table.

```sh
PLUMEGO_TEST_RETENTION_CAPACITY=1 go -C reference/with-scheduler test -v -run '^TestPostgresRetentionCapacity$'
```

## Result objects and progress

Apply schema v28. To run the optional `demo.export` task, set
`SCHEDULER_RESULT_DIR` to the same private, writable directory for the API,
the I/O Worker pool and the `result-gc` role. Use an application-owned
directory on a POSIX filesystem that supports durable file and directory
sync. Replicas need the same shared mount. Start the I/O pool with
`SCHEDULER_WORKER_POOL=io`, then start the collector with
`go -C reference/with-scheduler run . -role result-gc`.

Submit `{"topic":"demo.export","payload":{"size_bytes":2097152}}` using the
normal task API and idempotency key. The demonstration streams binary data
up to 8 MiB per task. The database stores the completion receipt, object
reference, byte count, and expiration. The file becomes downloadable only
after the current claim commits the result. Failed, canceled, and interrupted
uploads can leave staged data for the collector, with a 15-minute staging
window. The result lifetime defaults to 24 hours and is set by the publishing
Worker with `SCHEDULER_RESULT_RETENTION`.

`GET /runs/{id}/result` authenticates and checks the tenant before opening
the file. Object downloads support byte ranges and use private, no-store
responses. Expired results return 410 for their tenant even before physical
deletion. The collector removes expired bytes and preserves completed
result metadata. Tasks that produce only a receipt still return JSON.

`GET /runs/{id}` can include `progress_percent`. The Worker persists the
first sample. Later samples require both 250 milliseconds since the previous
write and an increase of at least five percentage points. Progress belongs
to the current claim. A committed export reports 100 even after its file
expires.

## Local Worker autoscaling

Apply schema v29 and run the independent supervisor on a POSIX host:

```sh
SCHEDULER_WORKER_POOL=io SCHEDULER_AUTOSCALE_MIN=1 SCHEDULER_AUTOSCALE_MAX=4 \
  go -C reference/with-scheduler run . -role autoscale
```

One supervisor owns a pool. It starts and reaps actual Worker processes with
the same database and pool configuration. Keep the pool under the same
service-manager process group so its children are cleaned up if the
supervisor is killed. Child workers disable fixed-address metrics and probe
listeners to avoid port collisions.

| Setting | Default |
| --- | --- |
| `SCHEDULER_AUTOSCALE_MIN` and `SCHEDULER_AUTOSCALE_MAX` | 1 and 4 workers |
| `SCHEDULER_AUTOSCALE_INTERVAL` | 1s |
| `SCHEDULER_AUTOSCALE_WINDOW` | 30s |
| `SCHEDULER_AUTOSCALE_TARGET_AGE` | 2s |
| `SCHEDULER_AUTOSCALE_SCALE_IN_DELAY` | 30s |
| `SCHEDULER_AUTOSCALE_DB_RESERVE` | 8 unreserved client sessions |
| `SCHEDULER_WORKER_DRAIN_TIMEOUT` | 10s |

The supervisor samples runnable count and age, excluding future, expired,
paused and confirmation-pending tasks. Counts saturate at 10,000. It also
samples recent arrivals, completed executions, active slots and the shared
downstream concurrency and start-rate budget. Failed or timed-out samples
hold the current process count. During a sustained idle period, the
supervisor signals one Worker at a time to stop claiming and drain using
the existing shutdown path. A drained Worker still counts against the
process cap until it exits.

## Isolated shell task pool

Enable admission for `sandbox.shell` on the API with
`SCHEDULER_SANDBOX_TASKS=1`. The feature is disabled by default. Submit a
version-1 run with a script of at most 16 KiB and no resource references.
Admission uses the same tenant authorization, idempotency key, and quota
as other task types.

```json
{"topic":"sandbox.shell","task_version":1,"payload":{"script":"test -d /tmp"}}
```

Deploy a separate Worker with `SCHEDULER_WORKER_POOL=sandbox`. Its default
concurrency is one and it consumes only the isolated topic. Configure its
local Docker daemon in an independent Linux VM or use the `runsc` runtime.
The control plane and normal Worker pools do not need a Docker socket.

| Setting | Value |
| --- | --- |
| `SCHEDULER_SANDBOX_SOCKET` | Absolute path to the dedicated Docker Unix socket |
| `SCHEDULER_SANDBOX_SCOPE` | Unique label shared by Workers and the cleanup process attached to this daemon |
| `SCHEDULER_SANDBOX_IMAGE` | Locally provisioned, trusted Linux image by immutable `sha256:...` image ID, with `/usr/bin/timeout` and `/bin/sh`, and without declared volumes or additional environment variables |
| `SCHEDULER_SANDBOX_RUNTIME` | `runsc`, or `runc` on a dedicated VM |
| `SCHEDULER_SANDBOX_BOUNDARY` | `dedicated-vm` when the operator has provisioned a separate VM for the `runc` execution boundary |

The Worker verifies Linux cgroup v2 CPU, memory, swap, and process controls,
validates the image, and starts a test container before claiming tasks.
Provision the image through the deployment pipeline. No job can choose an
image or require a registry pull. The Docker socket is privileged
infrastructure. Restrict access to the dedicated Worker and cleanup role,
and keep unrelated services and credentials off the execution daemon.

Each task gets a fresh non-root container with no added capabilities,
privilege escalation disabled, built-in seccomp, no network, no host
mounts, a read-only root, and an 8 MiB temporary filesystem. Resources
are capped at half a CPU, 64 MiB memory with no swap, and 16 processes.
The image's timeout terminates the job after eight seconds. The Worker
adds a separate twelve-second call deadline and forces cleanup on
cancellation or error. Script content is passed through stdin. Container
stdout and stderr are discarded. A successful run writes the normal
durable completion receipt. Command failure reaches a terminal failure;
runtime unavailability follows the bounded retry policy.

Run `go -C reference/with-scheduler run . -role sandbox-gc` as a separately
supervised process using the same socket and scope. It uses the daemon
clock to remove remaining containers older than thirty seconds, including
unstarted containers left by an interrupted create. It does not connect to
the scheduler database. The in-container deadline and automatic daemon
removal also apply when a Worker is forcibly stopped.

Set `PLUMEGO_TEST_SANDBOX=1`, the Worker sandbox settings, and a disposable
`PLUMEGO_TEST_POSTGRES_DSN` to run the real-runtime sandbox tests. The
resource verification fixture expects a Linux `runc` daemon with private
cgroup v2 and a trusted image providing standard Unix tools such as
`awk` and `dd`.

## Durable DAG workflows

Apply schema v30 and start one or more workflow dispatchers with
`go -C reference/with-scheduler run . -role workflow`. Existing Worker
pools execute node tasks according to their normal task contracts.

Send an authenticated `POST /workflows` with an `Idempotency-Key` and a
graph such as:

```json
{
  "nodes": [
    {"id":"prepare","task":{"topic":"demo.record","payload":{}}},
    {"id":"export","depends_on":["prepare"],"task":{"topic":"demo.compute","payload":{}}}
  ]
}
```

The API persists the graph and initial ready nodes before returning 202.
It accepts up to 64 nodes and 512 edges and rejects cycles, duplicate node
IDs and invalid dependencies. The same tenant, key and graph return the
same workflow ID. Use `GET /workflows/{id}` to inspect node states,
dependencies and the ordinary run IDs assigned at dispatch.

The queue transition and workflow transition commit together. A successor
becomes ready after every predecessor succeeds. An unsuccessful terminal node
skips its blocked descendants while independent branches continue. Dispatch
commits the queue insertion and its node linkage together. A restarted
dispatcher resumes ready nodes from PostgreSQL. A tenant at quota waits for
capacity while other workflows can dispatch. Individual workflow dead
letters remain visible. To retry a failed graph, create a new workflow with
a new idempotency key.

### Waiting for external signals

Apply schema v31. A node may specify a `wait` instead of a `task`:

```json
{
  "id":"approve",
  "depends_on":["prepare"],
  "wait":{"key":"review","actor":"approver","deadline":"2030-01-01T12:00:00Z"}
}
```

After its prerequisites succeed, the node enters `waiting` without
creating a queue task. The workflow dispatcher checks absolute deadlines
using PostgreSQL time. If the deadline passes without a signal, the node
fails and its blocked descendants are skipped.

Send `POST /workflows/{id}/signals` with `{"key":"review"}` before the
deadline. The request's authenticated tenant and API actor must match the
workflow and the actor named on the wait node. Run the sender's API with
its own credential and `SCHEDULER_API_ACTOR=approver`. The correlation key
identifies one wait within a workflow. It carries no authorization by itself.

An authorized signal can arrive while prerequisites are still running.
The API returns its persisted `received_at`. Repeating an accepted signal
returns the same receipt, including after the deadline. A new late signal
returns 410. Accepted signals, unresolved deadlines and newly ready
successors survive dispatcher downtime.

### Bounded fan-out and fan-in

Apply schema v32. A node may also specify a `fanout` template:

```json
{
  "id":"map",
  "fanout":{
    "count":1000000,
    "max_in_flight":64,
    "task":{"topic":"demo.record","payload":{}}
  }
}
```

The workflow dispatcher materializes up to 32 children per transaction,
with at most `max_in_flight` unconfirmed children for the node. The limit
accepts 1–256. It persists the next ordinal and terminal counters with queue
and child writes. Worker metadata carries `workflow_id`, `workflow_node`
and the one-based `workflow_ordinal`. A successful barrier makes its
successors eligible. A failed child is retained in the report while the
other children finish. A barrier with any failed or canceled child fails
and skips blocked successors.

`GET /workflows/{id}/fanouts/{node}/children?after=128` pages by ordinal,
returning up to 128 child run IDs, their states and result availability.
Use the existing run result endpoint with the child run ID.
`POST /workflows/{id}/fanouts/{node}/cancel` stops expansion immediately.
The workflow dispatcher cancels materialized children that can still be
canceled. Children with an already-committed result follow normal queue
finalization. The barrier can settle once the materialized children are
terminal. The number never created is `total - created`.

### Recoverable task checkpoints

Apply schema v33. CPU Workers handle `demo.checkpoint` submissions:

```json
{
  "topic":"demo.checkpoint",
  "payload":{"shards":2,"items_per_shard":8,"segment_size":3,"output_version":2}
}
```

The example divides deterministic output into independently committed
segments. It supports 1–32 shards, 1–10,000 items per shard, and 1–256 items
per segment, with output formats 1 and 2. Choose segment size and Worker
timeout together for large jobs. The output version is pinned to the run.

A short PostgreSQL transaction verifies the live claim and commits the
segment digest together with its shard cursor and claim generation. After
lease takeover, the new Worker reads each shard's last committed offset.
It recomputes any uncommitted segment. Finished segments retain their
original generation. The ordinary run result is committed after every
shard reaches its end. `GET /runs/{id}/checkpoints` returns the tenant's
output version, offsets and last committing generations. The demonstration
uses PostgreSQL for both its cursor and its segment output.

### Workflow compensation and manual intervention

Apply schema v34. A workflow task can use
`{"topic":"demo.reserve","payload":{"quantity":7}}`. The short Worker
commits the reservation, its result receipt and a compensation record
together. If the workflow fails, the workflow dispatcher schedules release
tasks for committed reservations in reverse order. Failed releases retry
through the normal Worker policy with a three-attempt budget. A release
that exhausts its budget moves to `manual` and holds earlier compensations.

`GET /workflows/{id}/compensations` lists the tenant's reservations and
compensation states. Pass the returned `next_after` as `after` for the
next page. The workflow retains its failure state after releasing its
reservations.

For manual intervention, set `SCHEDULER_COMPENSATION_ACTOR` to the trusted
actor name used by the operator API process. That process must authenticate
the operator's own credential and use the matching `SCHEDULER_API_ACTOR`.
Send `POST /workflows/{id}/compensations/{reservation_run_id}` with an
`Idempotency-Key` and either `{"action":"retry"}` or
`{"action":"release"}`. The former schedules a fresh compensation run.
The latter applies the same release directly. Both reuse the original
compensation effect key and write an audit entry. A repeated command key
with the same action returns the original successful status. This
reference uses a transactional PostgreSQL reservation and release.

### Versioned workflow definitions

Apply schema v35. Set `SCHEDULER_WORKFLOW_RELEASE_ACTOR` to the trusted
publisher's API actor. The publisher uses their own credential and a
matching `SCHEDULER_API_ACTOR`.

- Publish an immutable graph with
  `PUT /workflow-definitions/{name}/versions/{version}`. The body is the
  usual `{"nodes":[...]}` graph. Repeating the same version and graph is
  safe. A different graph for a published version returns 409.
- Activate a version with `PUT /workflow-definitions/{name}/active`, an
  `Idempotency-Key`, and `{"version":2,"expected_version":1}`. Use expected
  version 0 for the first activation. Roll back by activating a previously
  published version with a fresh key and the current expected version.
  Retrying an old command with its original key after a rollback returns
  its original success without applying that release again.
- Start a named workflow with `POST /workflows` and
  `{"definition_name":"review"}`. Optionally set `definition_version` to
  pin a specific version. The active version is resolved once for a new
  start; an idempotent retry keeps the original instance. Inline graphs
  continue to work.

`GET /workflows/{id}` shows the pinned definition and version. Each
instance stores its own graph and task contracts, so existing work
continues after changing the default. For a rollout that also changes
task versions, keep compatible Worker pools for both sets of queue topics
until the older in-flight instances have finished.

### Event-driven workflows

Apply schema v36 and run the event consumer alongside the workflow
dispatcher and Worker pools:

```sh
go -C reference/with-scheduler run . -role events
```

A release-authorized API actor configures a feed with
`PUT /event-sources/{source}`, for example:

```json
{
  "definition_name":"review",
  "definition_version":1,
  "ingest_actor":"producer",
  "max_lateness_seconds":3600
}
```

The source binds an explicit, immutable workflow definition version. Its
designated producer submits `POST /event-sources/{source}/events` with
`offset`, `id`, `occurred_at`, and `payload`. Offsets start at 1. The
API acknowledges durable receipt even if an earlier offset is missing.
It rejects a changed event for an existing offset or ID. A duplicate
event at a later offset is recorded and advances the cursor when reached.
An event is late if its occurrence predates its receipt by more than the
source's lateness budget. The cursor advances across late events without
starting a workflow.

`GET /event-sources/{source}` returns the committed cursor and highest
received offset. `GET /event-sources/{source}/events/{offset}` returns
payload and outcome. The resulting workflow exposes its event source and
offset. Consumer replicas commit each position, deduplication and workflow
creation together.

An authorized publisher can replay up to 1,000 committed positions with
`POST /event-sources/{source}/replays`, an `Idempotency-Key`, and
`{"from":1,"to":100}`. Follow the response's `Location` for persisted
replay progress. Replay preserves original late and duplicate outcomes
and reuses any original workflow. It leaves the live cursor unchanged.

## PostgreSQL tests

Set `PLUMEGO_TEST_POSTGRES_DSN` to a dedicated test database with permission
to create and drop test schemas.

```sh
go -C reference/with-scheduler test -race -count=1 ./...
```

For a process-level smoke test, use an empty disposable database, with Python on PATH.

```sh
python3 reference/with-scheduler/smoke.py
```

The smoke test starts and replaces API, scheduler and Worker processes
independently. It checks that both API replicas return the same run for a
repeated submission, the remaining scheduler continues materializing runs
after a replica exits, and a replacement Worker handles both task versions
after the v1-only Workers drain.

### PostgreSQL failover drill

Run the drill as a non-root user with Go, Python 3.8+, and PostgreSQL server
and client commands on PATH. It creates two disposable, loopback-only
clusters and an independent receiver database in a temporary directory.

```sh
PYTHONDONTWRITEBYTECODE=1 python3 reference/with-scheduler/failover_drill.py
```

The primary waits for the named standby to apply commits before acknowledging
them. The drill records an accepted task and result, a live task claim, and
an HTTP event whose receiver commits before disconnecting. It verifies the
candidate's replay horizon and stops the old primary before promoting the
standby. The API rejects mutations until synchronous protection returns.
The old endpoint is rebuilt as a read-only standby and a test write must
fail. Retry uncertain submissions with the original idempotency key.

The JSON report separates writer readiness, restored synchronous protection,
API readiness, new confirmation, claim recovery, backlog drain, Outbox
confirmation and final reconciliation. It reports delivery attempts and
business effects independently. A second promotion checks that receipts
accepted under the restored synchronous protection reach the next writer.

### PostgreSQL point-in-time recovery drill

The restore drill takes a base backup, archives WAL, and restores an isolated
cluster to a named point. It verifies the schema, plan cursor, live claim,
prior result and Outbox acknowledgement while delivery remains paused on the
restored cluster. A separate receiver retains a later inventory change. The
reconciliation role identifies that effect before any replay. The drill then
recreates the missing run with its original request and business keys,
resumes delivery, and verifies that reconciliation has no remaining differences
and the receiver applied the business effect only once.

```sh
PYTHONDONTWRITEBYTECODE=1 python3 reference/with-scheduler/restore_drill.py
```

### Capacity baseline

Run the complete local benchmark with the same prerequisites as the failover
drill. It creates and removes its own databases and service processes.

```sh
PYTHONDONTWRITEBYTECODE=1 python3 reference/with-scheduler/load_baseline.py > baseline.json
```

The JSON report covers sustained short tasks, long I/O, simultaneous
schedules, a hot tenant, first-attempt failures and a primary failover
with confirmed backlog. For the sample profile, metric definitions and
bottleneck observations, see [capacity baseline](load-baseline.md).
For mixed workload gates and connection budgets, see [mixed load certification](load-baseline.md#混合负载认证)
and the [production certification runbook](production-certification.md).

## Tenant capacity

A tenant starts with limits of 10,000 pending runs, 64 MiB of pending payloads,
and 1,000 schedule definitions. Version 6 backfills usage from current rows.
Queued and leased runs count against the same budget. Cancellation, ACK,
expiry, and dead-letter transitions release it. Requeueing a dead-letter run
reserves capacity again.

The API returns `429` with `Retry-After` for a new command that would exceed a
limit. A successful command can still be replayed with its original
idempotency key. A due periodic schedule whose tenant is full keeps its cursor
until capacity becomes available. Its misfire policy then determines catch-up.

Schema v39 allows a per-tenant write rate for direct, batch and manual trigger
commands. Only new instances consume it. The optional urgent budget is for
`high` priority submissions from the named deployment actor
(`SCHEDULER_API_ACTOR`). A batch exceeding either applicable budget rolls
back all new admissions. Automatic schedules retain their existing quota and
misfire rules. Configure a separate execution budget with scope `retry` and
the logical task type as its key to limit subsequent starts independently
of initial attempts.

Operators can set tenant-specific limits through the database before or after
the tenant's first request. For example, use parameters for the tenant and
limits in an operation equivalent to:

```sql
INSERT INTO scheduler_tenant_usage (tenant_id, max_pending_runs, max_pending_bytes, max_schedules)
VALUES ('tenant-a', 500, 4194304, 100)
ON CONFLICT (tenant_id) DO UPDATE
SET max_pending_runs = EXCLUDED.max_pending_runs,
    max_pending_bytes = EXCLUDED.max_pending_bytes,
    max_schedules = EXCLUDED.max_schedules;
```

## HTTP outbox delivery

To send to a separate service, configure the outbox role with
`SCHEDULER_OUTBOX_URL` and `SCHEDULER_OUTBOX_TOKEN`. The endpoint must use
HTTPS or a literal loopback IP for local testing. Configure all outbox
replicas for the same delivery mode and destination.

The sample receiver uses an independent PostgreSQL database configured via
`SCHEDULER_RECEIVER_DATABASE_URL`. Run the `receiver-migrate` role once, then
run `receiver -addr 127.0.0.1:8095` with the same token. For local use, set the
sender URL to `http://127.0.0.1:8095/events`.

The sender claims a short database lease, sends outside the transaction and
accepts a `204` confirmation. A timed-out lease can be reclaimed. The
receiver stores one receipt per event ID before acknowledging it. Regular
events send only that ID. Inventory events also carry the validated
business fields.

Inventory events add a versioned business command and transactional receiver
projection. See the [inventory and reconciliation runbook](production-certification.md#1-业务副作用与对账)
for setup, safe replay and consistency checks.

### Crash recovery verification

On Unix, the PostgreSQL integration suite starts real scheduler and Worker
child processes. It uses a disposable schema and advisory-lock barriers to
observe the failover boundaries. To run only these cases:

```sh
go -C reference/with-scheduler test -race -count=1 -run 'TestPostgres(WorkerProcess|SchedulerProcess)' ./...
```

The Worker tests kill one child before result commit, after commit but before
ACK, and after ACK. A replacement claims the first case after lease expiry.
A finalizer completes the second case from the stored intent. The scheduler
test covers killing the cursor transaction before commit and killing the
replacement after commit. Separate cases cover dead-letter persistence and
recovery after ACK, retry release, and dead-letter write failures.

The SIGTERM and timed SIGKILL tests exercise readiness, claim cutoff,
and takeover after natural lease expiry:

```sh
go -C reference/with-scheduler test -race -count=1 -run 'TestPostgresWorker(SIGTERM|TimedKill|DrainTimeout)' ./...
```

## Dead-letter history and replay

`GET /runs/:id/dead-letters` lists a run's failure records for the authenticated
tenant. It returns at most 50 entries, with `next_before` for the next page.
Entries show the job type, task and schedule versions, attempt, safe failure
code and timestamps. The API omits the original payload and error text.

`POST /dead-letters/replay` requires `failed_from` and `failed_to` as RFC 3339
timestamps, a `limit` from 1 to 25, and an explicit `preview` flag. The time
range is inclusive at the start and exclusive at the end, at most seven
days wide. Optional filters are `topic` and `failure_code`. Preview lists
candidates without consuming either quota or an idempotency key.

For an actual replay, set `preview` to false and supply an `Idempotency-Key`.
The tenant can requeue up to 25 runs per UTC minute. Each run can be manually
replayed twice. Replay preserves the run ID and cumulative attempt number,
granting one more execution attempt. A replay also reserves pending run and
payload capacity. Retrying the same command returns the original result.
Unresolved outcomes remain quarantined for reconciliation. Legacy records
without a provable attempt number are retained for inspection.

## Administrative audit

The API uses a configured `SCHEDULER_API_ACTOR` label (default `tenant-api`)
for the authenticated tenant's operations. It generates the request ID on
the server and includes it in the response. Mutating endpoints accept an
optional `X-Change-Reason` of `operator_request`, `maintenance`,
`incident_recovery`, or `correction`. With the header omitted the operation
provides the reason code.

The repository stores control changes in `scheduler_audit` in the same
transaction that updates the run or schedule. The entries contain tenant,
actor, request ID, target, reason, transition, and timestamp. Duplicate
commands do not add audit entries. Inspect this table with an authorized
database role. Queue attempt records remain separate from this operation log.
