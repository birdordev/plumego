ALTER TABLE mq_tasks ADD COLUMN execution_attempts integer NOT NULL DEFAULT 0
    CHECK (execution_attempts >= 0 AND execution_attempts <= attempts);
UPDATE mq_tasks SET execution_attempts = attempts WHERE attempts > 0;

ALTER TABLE mq_task_attempts ADD COLUMN execution_no integer CHECK (execution_no > 0),
    ADD COLUMN execution_started_at timestamptz,
    ADD CONSTRAINT mq_task_execution_marker CHECK ((execution_no IS NULL) = (execution_started_at IS NULL));

CREATE UNIQUE INDEX mq_task_execution_sequence ON mq_task_attempts (task_id, execution_no)
    WHERE execution_no IS NOT NULL;
