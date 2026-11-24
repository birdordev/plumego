ALTER TABLE mq_task_attempts
    ADD COLUMN execution_outcome text CHECK (execution_outcome IN ('succeeded', 'failed', 'unknown', 'not_started')),
    ADD COLUMN finalization_operation text CHECK (finalization_operation IN ('ack', 'release', 'dlq')),
    ADD COLUMN finalization_state text CHECK (finalization_state IN ('pending', 'confirmed', 'superseded')),
    ADD COLUMN finalization_reason text,
    ADD COLUMN finalization_retry_at timestamptz,
    ADD COLUMN finalization_check_at timestamptz,
    ADD COLUMN finalization_failure text CHECK (finalization_failure IN ('retryable', 'lease_lost', 'commit_unknown'));

CREATE INDEX scheduler_pending_finalizations ON mq_task_attempts (finalization_check_at, task_id, attempt_no)
    WHERE finalization_state = 'pending';
CREATE INDEX scheduler_pending_finalizations_run ON mq_task_attempts (task_id, attempt_no)
    WHERE finalization_state = 'pending';

CREATE OR REPLACE VIEW scheduler_blocked_claims AS
SELECT r.id AS task_id
FROM schedules s JOIN scheduled_runs r ON r.tenant_id = s.tenant_id AND r.schedule_id = s.id
WHERE s.claim_paused
UNION ALL
SELECT task_id FROM mq_task_attempts WHERE finalization_state = 'pending';

CREATE FUNCTION scheduler_protect_finalization() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM mq_task_attempts
       WHERE task_id = OLD.id AND attempt_no = OLD.attempts AND finalization_state = 'pending') THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END
$$;

-- 领取消费者与过期清理均须在行锁下保护已提交的确认意图。
CREATE TRIGGER scheduler_protect_finalization BEFORE UPDATE OF attempts, status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status = 'leased' AND (NEW.status = 'expired' OR
        (NEW.status = 'leased' AND NEW.attempts <> OLD.attempts)))
    EXECUTE FUNCTION scheduler_protect_finalization();

CREATE FUNCTION scheduler_resolve_finalization() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    UPDATE mq_task_attempts SET finalization_state = CASE
        WHEN (finalization_operation, NEW.status) IN (('ack','done'), ('release','queued'), ('dlq','dead'))
          THEN 'confirmed' ELSE 'superseded' END,
        finalization_check_at = NULL
    WHERE task_id = OLD.id AND attempt_no = OLD.attempts AND finalization_state = 'pending';
    RETURN NEW;
END
$$;

CREATE TRIGGER scheduler_resolve_finalization AFTER UPDATE OF status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status = 'leased' AND NEW.status <> 'leased')
    EXECUTE FUNCTION scheduler_resolve_finalization();
