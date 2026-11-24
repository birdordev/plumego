CREATE TABLE scheduler_bulk_operations (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_key text NOT NULL,
    topic text NOT NULL,
    selection_limit integer NOT NULL CHECK (selection_limit BETWEEN 1 AND 500),
    state text NOT NULL DEFAULT 'planned' CHECK (state IN ('planned', 'running', 'paused', 'completed')),
    actor text NOT NULL,
    request_id text NOT NULL,
    reason_code text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, request_key)
);

CREATE INDEX scheduler_bulk_operations_ready ON scheduler_bulk_operations (created_at, tenant_id, id)
    WHERE state = 'running';

CREATE TABLE scheduler_bulk_items (
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    run_id text NOT NULL,
    task_updated_at timestamptz NOT NULL,
    outcome text NOT NULL DEFAULT 'pending' CHECK (outcome IN ('pending', 'canceled', 'changed', 'missing')),
    PRIMARY KEY (tenant_id, operation_id, run_id),
    FOREIGN KEY (tenant_id, operation_id) REFERENCES scheduler_bulk_operations (tenant_id, id)
);

CREATE INDEX scheduler_bulk_items_pending ON scheduler_bulk_items (tenant_id, operation_id, run_id)
    WHERE outcome = 'pending';

CREATE INDEX scheduler_bulk_queued_selection ON mq_tasks (tenant_id, topic, available_at, id)
    WHERE status = 'queued';
