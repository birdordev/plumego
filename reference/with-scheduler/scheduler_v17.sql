CREATE TABLE scheduler_execution_slots (
    tenant_id text NOT NULL,
    scope text NOT NULL CHECK (scope IN ('schedule', 'key', 'tenant')),
    group_key text NOT NULL,
    task_id text NOT NULL DEFAULT '',
    lease_owner text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, scope, group_key)
);

ALTER TABLE mq_tasks DROP CONSTRAINT mq_tasks_status_check;
ALTER TABLE mq_tasks ADD CONSTRAINT mq_tasks_status_check
    CHECK (status IN ('queued', 'leased', 'done', 'dead', 'expired', 'canceled', 'skipped'));

DROP TRIGGER scheduler_record_delay_completion ON mq_tasks;
CREATE TRIGGER scheduler_record_delay_completion AFTER UPDATE OF status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status IN ('queued', 'leased') AND NEW.status IN ('done', 'dead', 'canceled', 'expired', 'skipped'))
    EXECUTE FUNCTION scheduler_record_delay_completion();
