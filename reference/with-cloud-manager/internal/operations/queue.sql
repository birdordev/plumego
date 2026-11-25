-- The queue schema is based on x/messaging/mq/store/migrations/postgres/001_task_queue.sql.
CREATE TABLE cmp_operation_tasks (
    id text PRIMARY KEY,
    topic text NOT NULL,
    tenant_id text NOT NULL,
    payload bytea,
    meta jsonb,
    priority integer NOT NULL,
    dedupe_key text,
    status text NOT NULL CHECK (status IN ('queued', 'leased', 'done', 'dead', 'expired')),
    attempts integer NOT NULL CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts > 0),
    available_at timestamptz NOT NULL,
    lease_owner text,
    lease_until timestamptz,
    expires_at timestamptz,
    last_error text,
    last_error_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (status <> 'leased' OR (lease_owner IS NOT NULL AND lease_until IS NOT NULL))
);

CREATE UNIQUE INDEX cmp_operation_tasks_tenant_dedupe ON cmp_operation_tasks (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX cmp_operation_tasks_ready ON cmp_operation_tasks (topic, available_at) WHERE status = 'queued';
CREATE INDEX cmp_operation_tasks_lease_expiry ON cmp_operation_tasks (lease_until) WHERE status = 'leased';
CREATE INDEX cmp_operation_tasks_task_expiry ON cmp_operation_tasks (expires_at) WHERE status IN ('queued', 'leased') AND expires_at IS NOT NULL;
CREATE INDEX cmp_operation_tasks_dead ON cmp_operation_tasks (updated_at, id) WHERE status = 'dead';

CREATE TABLE cmp_operation_dlq (
    entry_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id text NOT NULL,
    topic text NOT NULL,
    tenant_id text,
    payload bytea,
    meta jsonb,
    reason text NOT NULL,
    failed_at timestamptz NOT NULL
);

CREATE INDEX cmp_operation_dlq_task ON cmp_operation_dlq (task_id, failed_at, entry_id);

CREATE TABLE cmp_operation_attempts (
    task_id text NOT NULL,
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    reason text,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    success boolean NOT NULL DEFAULT false,
    PRIMARY KEY (task_id, attempt_no)
);
