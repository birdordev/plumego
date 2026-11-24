CREATE TABLE schedules (
    tenant_id text NOT NULL,
    id text NOT NULL,
    expression text NOT NULL,
    time_zone text NOT NULL,
    topic text NOT NULL,
    payload bytea NOT NULL,
    next_fire_at timestamptz NOT NULL,
    paused boolean NOT NULL DEFAULT false,
    version bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX schedules_due ON schedules (next_fire_at, tenant_id, id) WHERE NOT paused;

CREATE TABLE scheduled_runs (
    id text PRIMARY KEY,
    tenant_id text NOT NULL,
    schedule_id text,
    schedule_version bigint,
    scheduled_at timestamptz NOT NULL,
    request_key text,
    request_hash bytea,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, schedule_id) REFERENCES schedules (tenant_id, id),
    UNIQUE (tenant_id, request_key),
    UNIQUE (tenant_id, schedule_id, scheduled_at)
);

CREATE TABLE scheduled_results (
    run_id text PRIMARY KEY REFERENCES scheduled_runs (id),
    completed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
