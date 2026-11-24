CREATE TABLE scheduler_dead_letters (
    entry_id bigint PRIMARY KEY REFERENCES mq_task_dlq (entry_id) ON DELETE CASCADE,
    run_id text NOT NULL REFERENCES scheduled_runs (id),
    tenant_id text NOT NULL,
    schedule_id text,
    schedule_version bigint,
    attempt_no integer,
    topic text NOT NULL,
    task_version text NOT NULL,
    failure_code text NOT NULL,
    failed_at timestamptz NOT NULL,
    replayed_at timestamptz
);

CREATE INDEX scheduler_dead_letters_tenant_failed ON scheduler_dead_letters (tenant_id, failed_at, entry_id);
CREATE INDEX scheduler_dead_letters_run_history ON scheduler_dead_letters (tenant_id, run_id, entry_id DESC);
ALTER TABLE scheduled_runs ADD COLUMN dead_letter_replays integer NOT NULL DEFAULT 0;

CREATE TABLE scheduler_replay_budget (
    tenant_id text PRIMARY KEY,
    window_start timestamptz NOT NULL,
    used integer NOT NULL
);

CREATE TABLE scheduler_replay_commands (
    tenant_id text NOT NULL,
    command_key text NOT NULL,
    fingerprint bytea NOT NULL,
    response jsonb NOT NULL,
    PRIMARY KEY (tenant_id, command_key)
);

CREATE FUNCTION scheduler_failure_code(p_reason text)
RETURNS text LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE p_reason
        WHEN 'permanent task failure' THEN 'permanent'
        WHEN 'task outcome unknown' THEN 'outcome_unknown'
        WHEN 'task queue wait limit exceeded' THEN 'queue_timeout'
        WHEN 'task age limit exceeded' THEN 'age_limit'
        WHEN 'task retry would exceed age limit' THEN 'age_limit'
        ELSE 'attempts_exhausted'
    END
$$;

INSERT INTO scheduler_dead_letters
    (entry_id, run_id, tenant_id, schedule_id, schedule_version, attempt_no, topic, task_version, failure_code, failed_at)
SELECT d.entry_id, r.id, r.tenant_id, r.schedule_id, r.schedule_version,
    COALESCE((SELECT max(a.attempt_no) FROM mq_task_attempts a
              WHERE a.task_id = r.id AND a.finished_at = d.failed_at AND NOT a.success),
             CASE WHEN q.status = 'dead' AND q.last_error_at = d.failed_at THEN q.attempts END),
    d.topic, COALESCE(d.meta->>'task_version', '1'), scheduler_failure_code(d.reason), d.failed_at
FROM mq_task_dlq d
JOIN scheduled_runs r ON r.id = d.task_id AND r.tenant_id = d.tenant_id
LEFT JOIN mq_tasks q ON q.id = d.task_id AND q.tenant_id = d.tenant_id;

CREATE FUNCTION scheduler_capture_dead_letter()
RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    INSERT INTO scheduler_dead_letters
        (entry_id, run_id, tenant_id, schedule_id, schedule_version, attempt_no, topic, task_version, failure_code, failed_at)
    SELECT NEW.entry_id, r.id, r.tenant_id, r.schedule_id, r.schedule_version,
        q.attempts, NEW.topic, COALESCE(NEW.meta->>'task_version', '1'), scheduler_failure_code(NEW.reason), NEW.failed_at
    FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
    WHERE q.id = NEW.task_id AND q.tenant_id = NEW.tenant_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER scheduler_capture_dead_letter AFTER INSERT ON mq_task_dlq
    FOR EACH ROW EXECUTE FUNCTION scheduler_capture_dead_letter();
