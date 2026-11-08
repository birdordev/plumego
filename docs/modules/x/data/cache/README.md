# x/data/cache

> **Import path:** `github.com/birdordev/plumego/x/data/cache` — sub-package of [`x/data`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

## Purpose

`x/data/cache` provides extension-layer cache adapters and topology-heavy cache implementations. It builds on the stable `store/cache` abstractions.

## v1 Status

- `experimental` in the Plumego v1 support matrix
- Included in repository release scope, but compatibility is not frozen

## Use this module when

- implementing distributed caching with consistent hashing
- building ranked-data or leaderboard cache features on top of `store/cache`
- adapting a Redis client to the `store/cache.Cache` interface
- building topology-heavy or provider-specific cache backends

## Do not use this module for

- core in-memory caching (use `store/cache.MemoryCache`)
- tenant-aware cache scoping (use `x/tenant/store/cache`)
- adding tenant-specific logic to cache keys

## Sub-packages

- `x/data/cache/distributed` — consistent-hashing distributed cache with replication modes and failover strategies
- `x/data/cache/leaderboard` — in-memory ranked-data cache on top of stable `store/cache` primitives
- `x/data/cache/redis` — minimal Redis client adapter implementing `store/cache.Cache`

## Distributed constructor notes

- Prefer `distributed.NewWithConfig` when callers need invalid node or config
  errors.
- `distributed.New` is retained as a compatibility helper and returns `nil`
  when construction fails.
- `Close` is safe to call more than once. After `Close`, foreground cache
  operations, topology mutations, and node health reads return
  `distributed.ErrClosed`; `Nodes` and `GetMetrics` remain snapshot-style
  inspection methods.

## Distributed behavior

- Node `Weight()` values scale virtual-node placement in the hash ring. A
  single physical node is capped at a bounded virtual-node placement count; a
  pathological weight returns `distributed.ErrHashRingNodeTooLarge` instead of
  allocating unbounded ring state.
- Virtual-node hash collisions are resolved without overwriting existing ring
  entries and are exposed through `DistributedMetrics.HashCollisions`.
- Pathological virtual-node placement fails with
  `distributed.ErrHashRingSaturated` after a bounded collision probe window and
  rolls back the failed node add.
- `Config.HealthProbe` customizes node health checks. The default probe is a
  lightweight fallback that calls the wrapped cache `Exists` operation on an
  internal health-check key. Production backends whose miss/error semantics do
  not make `Exists` a reliable health signal should provide an explicit probe.
- Replica write failures are exposed through
  `DistributedMetrics.ReplicationFailures`, and the latest observed replica
  write duration is exposed through `DistributedMetrics.ReplicationLag`.
- `ReplicationNone` selects only the primary hash-ring node for `Set`,
  `Delete`, `Incr`, `Decr`, and `Append`; it does not require the configured
  secondary replica count to be satisfiable.
- `ReplicationSync` writes the primary first for `Set`, `Incr`, `Decr`, and
  `Append`, then writes selected secondary replicas synchronously. It returns
  an error when a selected replica is unhealthy or a replica write fails. It is
  not a strong-consistency or transaction contract: replicas that accepted a
  mutation before another replica failed are not rolled back.
- Synchronous `Set` secondary fanout is bounded by
  `Config.SyncReplicationMaxConcurrency`. When that field is zero, it falls
  back to `Config.AsyncReplicationMaxConcurrency` for compatibility with older
  call sites.
- `ReplicationAsync` writes the primary synchronously and schedules healthy
  secondary replicas through a bounded worker queue.
- `Config.AsyncReplicationTimeout` bounds each async secondary write. The
  default is 2 seconds, including defensive fallback for invalid internal
  timeout state.
- `Config.AsyncReplicationMaxConcurrency` bounds concurrent async secondary
  writes. `Config.AsyncReplicationQueueSize` bounds queued async secondary
  writes. When the queue is full or the cache is closed, the secondary write is
  dropped, `DistributedMetrics.ReplicationFailures` is incremented, and the
  optional `Config.AsyncReplicationDropHandler` receives the dropped operation,
  key, node ID, and drop reason.
- Zero values for `Config.FailoverRetryAttempts`,
  `Config.FailoverRetryBackoff`, `Config.AsyncReplicationTimeout`,
  `Config.AsyncReplicationMaxConcurrency`, and
  `Config.AsyncReplicationQueueSize` mean “use the package default”; negative
  values are rejected. Zero values for
  `Config.SyncReplicationMaxConcurrency` mean “use the normalized async
  replication concurrency as the compatibility default”; negative values are
  rejected.
- `Close` stops async workers and reports queued-but-unstarted async
  replication jobs through the same closed-drop callback path.
- Operations that require replicas fail with `distributed.ErrInsufficientReplicas`
  when the ring cannot satisfy the configured replica count.
- `Incr`, `Decr`, and `Append` follow the configured replication mode. In
  synchronous mode, the primary mutation happens before secondary mutations; if
  a secondary mutation fails, the returned value/error reports the partial
  outcome and the primary mutation remains visible.
- `Set` and `Append` copy caller-owned byte slices before passing them to node
  caches. The distributed boundary does not rely on the wrapped cache to protect
  writes from later caller slice mutation.
- `Get` copies byte slices returned from primary, failover, and retry paths
  before returning them to the caller. The distributed boundary does not expose
  wrapped node cache buffers directly.
- `Exists` uses the same failover strategy as `Get` when the primary returns an
  infrastructure error or is unhealthy. A primary cache miss remains a miss.
- `FailoverNextNode` reads from the selected replica set. With
  `ReplicationNone`, that selected set contains only the primary, so there is
  no secondary node for next-node failover.
- `FailoverAllNodes` may read from any healthy node in the ring.
- `FailoverRetry` retries the failed primary node when it is still healthy.
  `Config.FailoverRetryAttempts` and `Config.FailoverRetryBackoff` tune this
  path; zero values use the conservative defaults of 3 attempts and 10ms
  backoff.
- Nodes must have non-empty IDs and non-nil `store/cache.Cache` instances.
- `Set`, `Delete`, and `Clear` may return an error after partial side effects
  are already visible on replicas that accepted the mutation.
- `Clear` fails closed when no node can be cleared or any selected node fails.
  It is a best-effort destructive operation and does not roll back nodes that
  were already cleared.

Asynchronous replication is best-effort. It does not report secondary write
errors to the caller and does not currently provide durable retry or repair
hooks; inspect `DistributedMetrics.ReplicationFailures` and, when configured,
`Config.AsyncReplicationDropHandler` for observable timeout, drop, and secondary
write failure counts. Drop handlers run on the caller's scheduling path and
should avoid blocking. Panics from drop handlers are recovered and counted as
replication failures. Health-check failure callbacks are also panic-isolated so
callback failures do not terminate the checker loop.

## Leaderboard behavior

- `leaderboard.MemoryLeaderboardCache` is in-process only.
- Leaderboard behavior is Plumego-local ranked-data behavior, not a Redis
  sorted-set compatibility promise.
- `LeaderboardConfig` defaults are normalized on an internal constructor copy;
  caller-owned config values are not mutated.
- `LeaderboardConfig.EnableMetrics` defaults to true only when using
  `DefaultLeaderboardConfig`. Custom config literals keep the Go zero value, so
  `EnableMetrics: false` intentionally disables operation counters.
- Sorted-set operations validate context cancellation and stable `store/cache`
  key rules directly, without probing the underlying memory cache for key
  existence.
- Nil or empty members fail with `leaderboard.ErrInvalidMember`.
- Scores must be finite values; NaN and infinities fail with
  `leaderboard.ErrInvalidScore`.
- Explicitly invalid score ranges and non-negative rank ranges fail with
  `leaderboard.ErrInvalidRange`.
- `MaxLeaderboards` is enforced from tracked leaderboard count in the
  sorted-set creation path without full-map scans or concurrent over-admission.
- Failed first writes do not leave empty leaderboards behind.
- Missing leaderboards return zero for cardinality and range-removal count
  operations, and `ErrLeaderboardNotFound` for member/range read and direct
  member removal operations.
- Score range operations (`ZRangeByScore`, `ZCount`, and
  `ZRemRangeByScore`) scan the skiplist base level. Keep
  `MaxMembersPerSet` sized for bounded in-process work; the default is 10,000
  members per leaderboard.
- Skiplist level generation is owned by each in-memory skiplist instance.
  Performance baselines for the bounded score-range contract live in
  `x/data/cache/leaderboard/leaderboard_bench_test.go`; run
  `go test -run '^$' -bench 'LeaderboardCache(ZRangeByScore|ZCount|ScoreRangeFullScanBaseline)' ./x/data/cache/leaderboard`
  when changing range behavior.
- `Close` is nil-safe and idempotent.
- After `Close`, leaderboard-specific operations and leaderboard `Clear` return
  `leaderboard.ErrClosed`.
- `GetLeaderboardMetrics` returns operational telemetry rather than a strongly
  consistent point-in-time snapshot. Counters and current member totals may
  represent slightly different moments under concurrent mutation. It snapshots
  operation counters before counting current members so metrics collection does
  not hold the metrics lock while acquiring sorted-set locks.
- `LeaderboardMetrics` is an exported snapshot DTO. Internal locks and mutable
  counters are not part of the exported metrics value.
- `LeaderboardMetrics.ZIncrements` counts successful `ZIncrBy` mutations.
- Failed `ZIncrBy` operations that would produce an invalid score preserve the
  logical member score and cardinality, but do not promise no internal skiplist
  structural churn.
- `LeaderboardMetrics.ZRems` counts actual removed members, not requested
  member names.
- Leaderboards use `DefaultTTL` when created by sorted-set writes.
  `DefaultTTL == 0` means use the package default of one hour; set
  `leaderboard.NoExpirationTTL` to create non-expiring leaderboards. Expiration
  is lazy plus cleanup based, so concurrent operations observe an
  eventually-expired contract rather than a strongly consistent expiration
  snapshot.

## Redis adapter behavior

- `redis.Adapter` adapts caller-provided clients; it does not import a concrete
  Redis driver.
- Prefer `redis.NewValidatedAdapterWithOptions` for new call sites that need
  construction-time option validation; this is the canonical constructor for new
  base adapter wiring. For optional capability adapters, prefer
  `redis.NewValidatedCounterAdapterWithOptions`,
  `redis.NewValidatedAppenderAdapterWithOptions`, or
  `redis.NewValidatedAtomicAdapterWithOptions`. `redis.NewAdapterWithOptions`,
  `redis.NewAdapter`, `redis.NewCounterAdapter`, `redis.NewAppenderAdapter`,
  and `redis.NewAtomicAdapter` remain compatibility constructors.
- Options passed to `redis.NewAdapterWithOptions` are copied into
  constructor-owned behavior; exported fields remain for compatibility with
  older `redis.NewAdapter` call sites. Those exported compatibility fields must
  be treated as construction-time configuration and must not be mutated
  concurrently with cache operations. When an option is supplied, the option
  value wins over later compatibility-field mutation.
- Package-provided `redis.With*` options are the future stable constructor
  contract. Custom `redis.Option` functions are compatibility hooks: their
  effective field values are validated by
  `NewValidatedAdapterWithOptions`, but they do not create frozen internal
  option state and should not be used as the stable production contract.
- `NewValidatedAdapterWithOptions` rejects nil clients, negative
  `MaxKeyLength`, and invalid effective `ClearPrefix` values during
  construction.
- Redis key validation wraps stable `store/cache` key errors.
- Adapter `Get`, `Set`, and `Append` copy byte slices at the adapter boundary
  so caller mutation and client-owned buffers do not leak through the adapter
  contract.
- `Adapter.Capabilities` reports optional atomic, append, prefix-clear, and
  FlushDB behavior supported by the wrapped client and selected options.
- The minimal `redis.Client` interface supports get, set, delete, and exists.
- `Incr` and `Decr` require the wrapped client to implement
  `redis.Incrementer`; otherwise capability constructors return
  `redis.ErrAtomicUnsupported`.
- `Append` requires the wrapped client to implement `redis.Appender`; otherwise
  capability constructors return `redis.ErrAtomicUnsupported`.
- `Clear` fails closed by default. When `ClearPrefix` is configured it uses
  `redis.PrefixFlusher` and does not fall back to DB-wide flushing. Without a
  prefix it calls `FlushDB` only when the client implements `redis.Flusher` and
  `Adapter.AllowFlushDB` is explicitly enabled.

## Redis adapter compatibility matrix

| Concern | Required adapter contract | Stable behavior |
| --- | --- | --- |
| Cache miss mapping | Configure `redis.WithNotFound` or `redis.NewAdapter`'s compatibility mapper for the concrete driver miss error | `Get` maps configured misses to `store/cache.ErrNotFound`; otherwise it returns the raw client error |
| Basic cache operations | Client implements `redis.Client` | `Get`, `Set`, `Delete`, and `Exists` are available |
| Atomic integer mutation | Client also implements `redis.Incrementer` | `Incr` and `Decr` work; otherwise they return `redis.ErrAtomicUnsupported` |
| Append mutation | Client also implements `redis.Appender` | `Append` works; otherwise it returns `redis.ErrAtomicUnsupported` |
| Namespaced clear | Configure `redis.WithClearPrefix` and use a client implementing `redis.PrefixFlusher` | `Clear` removes only the configured namespace and never falls back to `FlushDB` |
| DB-wide clear | Configure `redis.WithAllowFlushDB(true)` and use a client implementing `redis.Flusher` | `Clear` calls `FlushDB`; this is opt-in and should be avoided for shared production databases |

Production Redis wiring should prefer a `redis.NewValidated*WithOptions`
constructor with an explicit namespace and miss mapper. `FlushDB` is suitable
only for isolated test or single-purpose databases where clearing every key is
intended.
Before promoting this adapter, record at least one concrete driver binding
against this matrix, including miss mapping, TTL behavior, prefix clear,
DB-wide clear opt-in, atomic mutation, and append support.

## Stable-readiness blockers

- No two-release exported API stability evidence has been recorded for
  `x/data/cache`; promotion should select one child surface rather than the whole
  module root.
- Distributed cache async replication remains best-effort and has no durable
  repair contract. Async writes are bounded by timeout, worker concurrency, and
  queue size, and dropped work can be observed through metrics and an optional
  callback.
- Current-head exported API snapshots for distributed, leaderboard, and Redis
  surfaces are recorded under `docs/evidence/extension/snapshots/x-cache/`.
  They do not replace release-ref comparisons.
- Leaderboard behavior is explicitly Plumego-local ranked-data behavior with
  bounded in-process score range scans, not a Redis compatibility promise.
- Redis adapter behavior depends on caller-provided client implementations. The
  adapter now documents the expected interface matrix, miss mapping, destructive
  clear policy, option validation, byte-slice ownership, and capability
  reporting, but no concrete driver integration evidence is recorded.
- `Clear` can be namespaced through `PrefixFlusher`, but DB-wide `FlushDB`
  remains available when explicitly enabled; stable promotion still needs real
  client integration evidence for the documented production contract.
- Owner sign-off, release refs, and historical API snapshot comparisons are
  still missing.

Eighth-pass stabilization evidence is recorded in
`docs/evidence/extension/x-cache.md`. `x/data/cache/module.yaml` remains
`experimental` until the extension stability policy is satisfied.

## First files to read

- `x/data/cache/module.yaml`
- `x/data/cache/distributed/distributed.go`
- `x/data/cache/leaderboard/leaderboard.go`
- `x/data/cache/redis/redis.go`

## Canonical change shape

- implement `store/cache.Cache` interface
- keep topology decisions in this layer, not in stable store
- keep feature-specific cache behavior in this layer, not in stable store
- keep provider-specific logic isolated to sub-packages
- prefer `distributed.NewE` when constructing distributed caches from dynamic
  node sets so duplicate or invalid nodes are reported explicitly
- treat distributed counter/appender operations as primary-node mutations only;
  replicated configurations should fail closed unless a true cross-node atomic
  strategy is added
- use `redis.NewCounterAdapter`, `redis.NewAppenderAdapter`, or
  `redis.NewAtomicAdapter` when Redis clients support optional cache
  capabilities; the base `redis.Adapter` intentionally exposes only
  `store/cache.Cache`

## Boundary rules

- `x/data/cache` extends stable `store/cache` with topology-heavy or provider-specific backends; do not duplicate these in stable `store/cache`
- keep consistent-hashing and replication logic inside `x/data/cache/distributed`; do not push topology decisions into stable roots
- keep provider-specific client logic (Redis, future backends) isolated to their sub-packages; do not let provider details leak through the `store/cache.Cache` interface
- tenant-aware cache scoping belongs in `x/tenant/store/cache`; do not add tenant logic to `x/data/cache`
- do not add feature-specific ranked-data or leaderboard logic to stable `store/cache`; keep it in `x/data/cache/leaderboard`
