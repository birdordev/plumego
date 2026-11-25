CREATE TABLE scheduler_inventory (
    tenant_id text NOT NULL,
    sku text NOT NULL,
    available bigint NOT NULL CHECK (available >= 0),
    PRIMARY KEY (tenant_id, sku)
);

CREATE TABLE scheduler_inventory_effects (
    tenant_id text NOT NULL,
    operation_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('reserve', 'release')),
    sku text NOT NULL,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1000000),
    run_id text NOT NULL UNIQUE REFERENCES scheduled_runs (id),
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, operation_id, action),
    FOREIGN KEY (tenant_id, sku) REFERENCES scheduler_inventory (tenant_id, sku)
);

ALTER TABLE scheduled_outbox ADD COLUMN inventory_effect jsonb;

CREATE TABLE scheduler_inventory_redrive_limits (
    tenant_id text PRIMARY KEY,
    next_at timestamptz NOT NULL
);

CREATE TABLE scheduler_inventory_redrives (
    tenant_id text NOT NULL,
    command_key text NOT NULL,
    run_id text NOT NULL REFERENCES scheduled_runs (id),
    actor text NOT NULL,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, command_key)
);

CREATE TABLE scheduler_tenant_ingress (
    tenant_id text PRIMARY KEY,
    regular_per_second integer NOT NULL CHECK (regular_per_second BETWEEN 0 AND 1000000),
    urgent_per_second integer NOT NULL DEFAULT 0 CHECK (urgent_per_second BETWEEN 0 AND 1000000),
    urgent_actor text,
    bucket_started_at timestamptz,
    regular_used integer NOT NULL DEFAULT 0,
    urgent_used integer NOT NULL DEFAULT 0,
    CHECK (urgent_per_second = 0 OR (urgent_actor IS NOT NULL AND urgent_actor <> ''))
);

ALTER TABLE scheduler_execution_budgets DROP CONSTRAINT scheduler_execution_budgets_scope_check;
ALTER TABLE scheduler_execution_budgets ADD CONSTRAINT scheduler_execution_budgets_scope_check
    CHECK (scope IN ('tenant', 'type', 'downstream', 'retry'));

CREATE INDEX scheduler_tenant_ready_claim ON mq_tasks (tenant_id, topic, available_at, priority DESC, created_at, id)
    WHERE status = 'queued';
CREATE INDEX scheduler_tenant_expired_claim ON mq_tasks (tenant_id, topic, lease_until)
    WHERE status = 'leased';
