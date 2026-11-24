CREATE TABLE scheduler_archived_queue (
    id text PRIMARY KEY,
    tenant_id text NOT NULL,
    status text NOT NULL CHECK (status IN ('done', 'dead', 'canceled', 'expired')),
    attempts integer NOT NULL,
    execution_attempts integer NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE TABLE scheduler_archived_attempts (LIKE mq_task_attempts);
ALTER TABLE scheduler_archived_attempts ADD PRIMARY KEY (task_id, attempt_no);
CREATE TABLE scheduler_archived_audit (LIKE scheduler_audit);
ALTER TABLE scheduler_archived_audit ADD PRIMARY KEY (id);
CREATE INDEX scheduler_archived_audit_tenant_time ON scheduler_archived_audit (tenant_id, occurred_at, id);

CREATE TABLE scheduler_archive_totals (
    status text PRIMARY KEY,
    total bigint NOT NULL CHECK (total >= 0)
);
INSERT INTO scheduler_archive_totals (status, total)
VALUES ('done', 0), ('dead', 0), ('canceled', 0), ('expired', 0);

CREATE INDEX scheduler_archive_queue_due ON mq_tasks (updated_at, id)
    WHERE status IN ('done', 'dead', 'canceled', 'expired');
CREATE INDEX scheduler_archive_attempt_due ON mq_task_attempts (COALESCE(finished_at, started_at), task_id, attempt_no)
    WHERE finalization_state IS DISTINCT FROM 'pending';
CREATE INDEX scheduler_archive_audit_due ON scheduler_audit (occurred_at, id);

CREATE VIEW scheduler_queue_history AS
    SELECT id, tenant_id, status, attempts, execution_attempts, available_at FROM mq_tasks
    UNION ALL
    SELECT id, tenant_id, status, attempts, execution_attempts, NULL::timestamptz FROM scheduler_archived_queue;

CREATE VIEW scheduler_attempt_history AS
    SELECT * FROM mq_task_attempts
    UNION ALL
    SELECT * FROM scheduler_archived_attempts;
