ALTER TABLE mq_tasks DROP CONSTRAINT mq_tasks_status_check;
ALTER TABLE mq_tasks ADD CONSTRAINT mq_tasks_status_check
    CHECK (status IN ('queued', 'leased', 'done', 'dead', 'expired', 'canceled'));

CREATE TABLE scheduled_outbox (
    run_id text PRIMARY KEY REFERENCES scheduled_results (run_id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    delivered_at timestamptz
);

CREATE INDEX scheduled_outbox_pending ON scheduled_outbox (created_at, run_id)
    WHERE delivered_at IS NULL;

CREATE TABLE scheduled_deliveries (
    run_id text PRIMARY KEY REFERENCES scheduled_results (run_id),
    delivered_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO scheduled_outbox (run_id, created_at)
    SELECT run_id, completed_at FROM scheduled_results;
