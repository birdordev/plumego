# SQL task queue schema

Apply [PostgreSQL migration 001](../x/messaging/mq/store/migrations/postgres/001_task_queue.sql)
to a new database in a migration transaction before starting any worker with the default SQL store table names.
The migration creates the queue, dead-letter history, and optional attempt log.
Manage its version with your deployment's migration tool. For an existing
database, compare schemas and write an upgrade migration before enabling workers.

The queue requires a caller-owned PostgreSQL driver and connection pool. Use
`SQLConfig.Dialect = DialectPostgres`. The bootstrap migration targets PostgreSQL.
The MySQL adapter requires equivalent dialect-specific tables and indexes.

Use one globally unique task ID and, when applicable, a unique deduplication key
per tenant. Dead-letter rows record past failures even if a task is replayed.
Enable attempt logging only with an appropriate retention policy. A replay that
resets attempt numbers can reuse existing attempt numbers, so leave the counter
intact where attempt history is required.

For PostgreSQL, `SQLConfig.DeferredExpiration` moves TTL updates out of the
claim transaction. Run `SQLStore.ExpireBatch` with a bounded limit for each of
the queued and leased partitions. Each batch skips locked rows. The leased
partition skips live leases. Claims still filter task TTLs, and quota or status
changes take effect when the sweep commits. See the scheduling reference's
[expiry role](../reference/with-scheduler/README.md) and
[claim and expiry indexes](../reference/with-scheduler/scheduler_v12.sql).
