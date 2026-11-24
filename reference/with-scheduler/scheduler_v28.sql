CREATE TABLE scheduler_result_objects (
    id text PRIMARY KEY,
    tenant_id text NOT NULL,
    run_id text NOT NULL REFERENCES scheduled_runs (id),
    state text NOT NULL CHECK (state IN ('staging', 'ready', 'expired')),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 8388608),
    expires_at timestamptz NOT NULL
);

CREATE INDEX scheduler_result_objects_due ON scheduler_result_objects (expires_at, id)
    WHERE state IN ('staging', 'ready');

ALTER TABLE scheduled_results ADD COLUMN object_id text UNIQUE REFERENCES scheduler_result_objects (id);

CREATE TABLE scheduler_run_progress (
    run_id text PRIMARY KEY REFERENCES scheduled_runs (id),
    claim_no integer NOT NULL CHECK (claim_no > 0),
    percent integer NOT NULL CHECK (percent BETWEEN 0 AND 99)
);
