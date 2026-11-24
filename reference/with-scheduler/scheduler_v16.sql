ALTER TABLE schedules ADD COLUMN task_options jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(task_options) = 'object');

CREATE OR REPLACE FUNCTION scheduler_capture_dead_letter()
RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    INSERT INTO scheduler_dead_letters
        (entry_id, run_id, tenant_id, schedule_id, schedule_version, attempt_no, topic, task_version, failure_code, failed_at)
    SELECT NEW.entry_id, r.id, r.tenant_id, r.schedule_id, r.schedule_version,
        q.attempts, CASE WHEN NEW.topic = 'demo.record.v2' THEN 'demo.record' ELSE NEW.topic END,
        CASE COALESCE(NEW.meta->>'task_version', '1') WHEN '1' THEN '1' WHEN '2' THEN '2' ELSE 'unknown' END,
        scheduler_failure_code(NEW.reason), NEW.failed_at
    FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
    WHERE q.id = NEW.task_id AND q.tenant_id = NEW.tenant_id;
    RETURN NEW;
END;
$$;
