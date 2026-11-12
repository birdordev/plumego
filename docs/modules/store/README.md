# store

## Storage Decision Guide

| Scenario | Package |
|---|---|
| In-process memory cache (dev/test/local) | `store/cache` |
| Redis / distributed cache | `x/data/cache/redis` |
| Distributed cache with replication | `x/data/cache/distributed` |
| Ranked / leaderboard cache | `x/data/cache/leaderboard` |
| HTTP response cache (reverse proxy layer) | `x/gateway/cache` |
| Exact-key LLM response cache | `x/ai/llmcache` |
| Semantic similarity cache (AI) | `x/ai/semanticcache` |
| Local file read/write (contract layer) | `store/file` |
| S3 / image processing / signed URLs | `x/data/file` |
| File HTTP upload/download transport | `x/fileapi` |
| In-process idempotency check | `store/idempotency` |
| Durable idempotency (SQL/KV-backed) | `x/data/idempotency` |
| Small embedded key-value (file-backed) | `store/kv` |
| Durable KV engine (WAL, snapshots) | `x/data/kvengine` |
| SQL query helpers | `store/db` |
| pgx-specific helpers | `x/data/pgx` |
| sqlx-specific helpers | `x/data/sqlx` |
| Read-write split / sharding topology | `x/data/rw` / `x/data/sharding` |

## Purpose

`store` holds persistence primitives and base abstractions.

## v1 Status

- `ga` in the Plumego v1 support matrix
- v1 normalization removes compatibility aliases and no-error wrappers before
  the final stable surface is frozen

## Use this module when

- defining stable storage contracts
- implementing basic persistence helpers
- working below topology-heavy data features

## Do not use this module for

- tenant-aware storage policy
- tenant-aware adapters
- sharding or heavy topology defaults in stable roots
- app bootstrap

## First files to read

- `store/module.yaml`
- the target package under `store/*`
- `specs/repo.yaml`

## Canonical change shape

- keep interfaces narrow
- keep concurrent behavior testable
- move topology-heavy features to owning extensions
- keep cache and KV constructors explicit about validation errors; do not panic on invalid config
- `store/cache` reports empty or unsafe request keys with `cache.ErrInvalidKey`, while cache configuration errors remain classified under `cache.ErrInvalidConfig`.
- `store/cache` and `store/kv` treat `nil` values and non-nil empty byte slices as existing caller-owned values; missing keys remain distinguishable through not-found errors and existence checks.
- `store/cache.MemoryCache.Delete` is idempotent for missing keys and returns nil after validating the key and lifecycle state; `store/kv.KVStore.Delete` returns `kv.ErrKeyNotFound` when the key is missing.
- `store/cache.Incr` and `store/cache.Decr` create missing keys as integer values, but existing empty byte values are non-integers and return `cache.ErrNotInteger`.
- `store/cache.MemoryCache.Stats` returns a point-in-time snapshot of tracked entries, payload bytes, and closed lifecycle state; it does not mutate the cache or export provider-specific metrics.
- `store/cache.MemoryCache` expired-entry cleanup scans the whole in-process map on each cleanup pass instead of stopping at an arbitrary entry cap; this keeps cleanup predictable for stable in-process use.
- `store/cache.MemoryCache` and `store/kv.KVStore` are constructor-only objects; zero-value or nil receiver operations fail closed with the package closed-store sentinel where practical.
- `store/cache.MemoryCache.Close` closes the cache lifecycle; it waits for the cache write boundary before returning, and later operations return `cache.ErrCacheClosed`.
- keep DB analytics, summaries, instrumentation wrappers, pool-stat polling, and slow-query inspection out of `store/db`; route them to `x/observability/dbinsights`
- keep DB health payloads, open-retry loops, and generic timeout policy helpers out of `store/db`; callers own operation deadlines through `context.Context`
- keep HTTP response caching, request-derived cache keys, and cache metrics/introspection ownership out of `store/cache`
- keep signed URLs, metadata-manager ownership, uploader/image metadata, and file path/id helper policy out of `store/file`; route them to `x/data/file` and `x/fileapi`
- keep durable KV-engine concerns such as WAL, snapshots, serializer selection, compression, and shard tuning out of `store/kv`; route them to `x/data/kvengine`
- keep durable idempotency providers, SQL dialect policy, and table schema policy out of `store/idempotency`; route them to `x/data/idempotency`

## Behavior Matrix

| Package | Missing read | Expired read | Missing delete | Invalid key/path | Closed behavior | Nil context |
|---|---|---|---|---|---|---|
| `store/cache` | `Get` returns `ErrNotFound`; `Exists` returns `false, nil` | `Get` returns `ErrNotFound`; `Exists` returns `false, nil` after best-effort cleanup | `Delete` returns nil | Empty or unsafe keys return `ErrInvalidKey`; some validation paths also wrap `ErrInvalidConfig` | Mutations and reads return `ErrCacheClosed` | Accepted as no cancellation signal |
| `store/kv` | `Get` returns `ErrKeyNotFound`; `ExistsContext` returns false | `Get` returns `ErrKeyExpired` without pruning; read-only context helpers ignore expired entries | `Delete` returns `ErrKeyNotFound` | Empty keys return `ErrInvalidKey`; invalid options return validation errors | Mutations, value reads, and read-only context helpers return `ErrStoreClosed` | Accepted as no cancellation signal |
| `store/file` | Concrete backends should report `ErrNotFound` or wrap it in `*file.Error` | Not modeled in the stable interface | Concrete backends should report `ErrNotFound` or wrap it in `*file.Error` | Invalid paths return `ErrInvalidPath` or wrap it in `*file.Error` | Backend-owned | Implementations receive and should honor the caller context |
| `store/idempotency` | `Get` returns `found=false, nil`; terminal operations return `ErrNotFound` | `Get` returns `found=false, nil`; `Complete` returns `ErrNotFound` after cleanup | `Delete` returns `ErrNotFound` | Empty keys return `ErrInvalidKey` | Backend-owned | Implementations receive and should honor the caller context |
| `store/db` | `ScanRow` maps `sql.ErrNoRows` to `ErrNoRows`; `QueryRowStrict` returns `ErrNoRows` | Not modeled | Not modeled | Invalid config returns `ErrInvalidConfig`; nil DB returns operation-specific sentinel wrappers | `*sql.DB` lifecycle is caller-owned | Passed through exactly to `database/sql` |

## File Boundary

- `store/file` is the stable contract layer for file storage interfaces, shared file types, and errors.
- `store/file` is contract-only and does not bundle a local filesystem, object storage, tenant-aware, or HTTP-facing backend implementation.
- `store/file` metadata clone helpers detach common nested mutable values such as `map[string]any`, `map[string]string`, `[]any`, `[]string`, and `[]byte`.
  Self-references and cross-references through `map[string]any` and `[]any`
  are preserved within the detached copy instead of recursing indefinitely.
  Graph traversal uses an explicit worklist, so deep nesting does not grow the
  call stack or truncate metadata. Memory remains proportional to the copied
  graph; callers own input-size limits and must not mutate it during cloning.
  Arbitrary custom values remain caller-owned; this is not a general-purpose
  serializer or validation policy.
- `x/data/file` is the tenant-aware implementation layer for local/S3 storage backends, provider-specific config, metadata persistence, and thumbnail/image-processing helpers.
- `x/fileapi` is the HTTP transport layer for upload, download, info, delete, list, and temporary URL endpoints.
- Do not move tenant-aware path policy, metadata query parameter types, backend-specific behavior, or image-processing pipelines into stable `store/file`.
- Do not move HTTP handlers or multipart parsing into stable `store`.
- `store/file.Storage` defines transport-agnostic operations only; concrete backends must document list ordering, copy overwrite behavior, metadata preservation, and missing-delete behavior.
- `PutOptions.Metadata` and `File.Metadata` are caller-owned unless a backend explicitly documents defensive-copy behavior.
- Stable file backends should expose missing-path and invalid-path failures through `ErrNotFound` and `ErrInvalidPath`, either directly or through `*file.Error`.
- Tenant-aware local file backends must validate generated upload path components and verify final filesystem paths stay inside the configured storage root.
- Local file backends must bound per-upload disk writes; `x/data/file.LocalConfig.MaxUploadSize` defaults to 32 MiB and oversized uploads expose `ErrInvalidSize`.
- S3-compatible file backends must bound per-upload buffering; `x/data/file.S3Config.MaxUploadSize` defaults to 32 MiB and oversized uploads expose `ErrInvalidSize`.

## KV Boundary

- `store/kv` is the stable small embedded KV primitive for file-backed key/value persistence, TTL-aware CRUD, key scans, and basic stats.
- `store/kv.NewKVStore` requires an explicit `Options.DataDir`; it does not create a relative default data directory for `Options{}`.
- `store/kv` exposes context-aware inspection operations for callers that need
  cancellation or error visibility. `Set`, `Get`, and `Delete` remain
  synchronous convenience methods; `Exists`, `Keys`, `Size`, and `GetStats`
  were removed in favor of their context-aware forms.
- `store/kv` context-aware operations check cancellation before lock acquisition and again after lock acquisition; once full-state filesystem persistence begins, that filesystem phase is not interruptible.
- `store/kv` restores staged changes, including capacity evictions, when cancellation is observed before persistence or a write fails before the state file is replaced. After replacement, a directory-sync failure is still returned, but memory retains the published state. Such an error means durability is unconfirmed, not that the mutation was undone; inspect current state before retrying. A repeated delete of an already published deletion returns `ErrKeyNotFound`.
- `store/kv` computes one TTL deadline before persistence and uses it in memory and on disk. Slow persistence can consume the TTL; successful writes do not extend only the in-memory lifetime.
- `store/kv` persists the full in-memory state as JSON on each write, fsyncs the temporary state file, atomically replaces the state file, and syncs the parent directory when the platform supports it; it is intended for small embedded datasets rather than high-throughput durable-engine workloads.
- `store/kv` does not rewrite the state file during startup normalization; expired or over-capacity records can be pruned from memory without mutating disk until the next caller-initiated write.
- `store/kv` read operations do not persist expired-key cleanup as a side effect.
- `store/kv` classifies state-file JSON decode failures and invalid persisted keys with `kv.ErrCorruptState`; invalid-key corruption also keeps `kv.ErrInvalidKey` in the error chain for diagnostics.
- `store/kv` treats invalid persisted keys as state corruption and fails startup instead of silently dropping records.
- `x/data/kvengine` owns durable-engine behavior such as WAL, snapshots, serializer formats, compression, and shard/flush tuning.
- Do not add engine-format plumbing, snapshot APIs, or durability-tuning knobs back into stable `store/kv`.
- `store/kv` write persistence is synchronous; use caller contexts on
  `SetContext`, `DeleteContext`, and read-only context helpers when
  cancellation must be observed before the filesystem write boundary.
- `store/kv` uses package name `kv`; examples may alias the import as `kvstore` only to avoid local name collisions.
- `store/kv.DefaultOptions(dataDir)` is the public helper for the current
  embedded defaults while keeping `DataDir` explicit; callers can start from it
  and then override limits as needed.
- `NewKVStore` requires an explicit `Options.DataDir` and returns a validation
  error for invalid options; it must not silently write to the process working
  directory.
- Default capacity for the embedded state file helper is 100000 entries and
  200 MiB. Callers needing different bounds should pass explicit limits after
  measuring, and callers needing durable-engine tuning should use
  `x/data/kvengine`.
- `store/kv` uses a single JSON state file replaced with `os.Rename`; each mutation rewrites the full normalized state and is O(N) in the number of retained entries.
- Keys must be valid UTF-8. Mutations reject invalid keys with `ErrInvalidKey` before writing the JSON state file. Invalid keys in the decoded persisted state fail load with an `ErrInvalidKey`-wrapped error.
- `store/kv` does not provide cross-process locking, WAL, snapshots, or crash-recovery tuning. Parent-directory sync is attempted after atomic replacement and tolerates unsupported-directory-sync errors.
- A non-positive TTL means no expiration. `Get` leaves expired entries unchanged and returns
  `ErrKeyExpired`; read-only helpers such as `ExistsContext`, `KeysContext`,
  `SizeContext`, and `GetStatsContext` ignore expired keys without mutating
  persisted state.
- `Delete` returns `ErrKeyNotFound` for missing keys. `Close` is idempotent;
  after close, value reads, mutations, and read-only inspection return
  `ErrStoreClosed`.

## Idempotency Boundary

- `store/idempotency` is the stable primitive contract for idempotency records, statuses, errors, and the minimal `Store` interface.
- `store/idempotency.HashAwareStore` is the optional completion extension for providers that validate `RequestHash` during completion without changing the base `Store` interface.
- `store/idempotency.ValidateCompletion` fixes stable completion classification: mismatched request hashes return `ErrRequestMismatch`, same-hash completed records return `ErrAlreadyCompleted`, and expired records return `ErrExpired` before duplicate-completion replay decisions.
- `x/data/idempotency` owns durable KV/SQL provider implementations, SQL dialect policy, table naming, and duplicate-key handling.
- Do not add provider-specific adapters, table schema policy, or feature-specific dedupe rules back into stable `store/idempotency`.
- `store/idempotency` persists `RequestHash` but does not decide hash-conflict or replay policy; callers compare hashes after `Get` when the business operation requires it.
- `PutIfAbsent` reports whether the current call claimed the key; `false, nil` means a usable record or backend duplicate prevented the claim, not that the request is safe to replay without inspecting the record.
- Terminal cleanup operations are deterministic: missing or expired `Complete` returns `ErrNotFound`, and missing `Delete` returns `ErrNotFound`.
- SQL-backed idempotency providers must validate table identifiers and dialect values before query construction; table names are provider configuration, not caller input.

## DB Boundary

- `store/db` helpers execute with the exact `context.Context` supplied by the caller.
- `store/db.WithTransaction` commits successful callbacks and rolls back failed callbacks. Panic and `runtime.Goexit` paths also release the transaction; panics propagate unchanged. Callback and explicit rollback errors remain inspectable with `errors.Is`.
- `store/db.Open` initializes a `*sql.DB` handle and does not prove connectivity; use `Ping` when startup validation is required.
- `store/db.QueryRow` mirrors `database/sql.QueryRowContext` and defers query or scan errors until the returned row is scanned.
- `store/db.QueryRowContext` returns an explicit `ErrQueryFailed`-wrapped error for nil database inputs instead of returning a nil row.
- Query and transaction helpers must not infer deadlines from optional config interfaces.
- Use `context.WithTimeout` or `context.WithDeadline` at the application or owning extension boundary when an operation deadline is required.
- tenant configuration schema and migrations belong in `x/tenant/config`, not stable `store/db`

## Extension-layer cache implementations

Topology-heavy and provider-specific cache implementations have been migrated out of the stable root and now live in `x/data/cache`:

- `x/data/cache/distributed` — consistent-hashing distributed cache with replication and failover
- `x/data/cache/leaderboard` — ranked-data cache built on top of stable `store/cache` primitives
- `x/data/cache/redis` — Redis client adapter implementing `store/cache.Cache`

Current rule:

- do not add new topology-heavy or provider-heavy siblings under stable `store/cache`
- in-process cache cleanup scans all entries on each cleanup tick so expired entries do not remain solely because of a fixed scan cap
- do not add HTTP response caching middleware or request-derived cache helpers under stable `store/cache`
- do not add tenant-aware adapters or tenant-specific storage policy under stable `store`
- route new topology-heavy cache capabilities to `x/data/cache`
- route HTTP response caching to `x/gateway/cache`
- route tenant-aware cache adapters to `x/tenant/store/cache`

## v1 Breaking Normalization

- `store/cache.ErrNotFound` is the only cache-miss sentinel;
  `ErrCacheMiss` has been removed.
- `store/kv.Exists`, `Keys`, `Size`, and `GetStats` have been removed because
  they collapsed invalid key, closed-store, and cancellation errors. Use
  `ExistsContext`, `KeysContext`, `SizeContext`, and `GetStatsContext`.
