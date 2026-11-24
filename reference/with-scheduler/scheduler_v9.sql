CREATE TABLE scheduler_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    actor text NOT NULL,
    request_id text,
    operation text NOT NULL,
    target_id text NOT NULL,
    reason_code text NOT NULL,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX scheduler_audit_tenant_time ON scheduler_audit (tenant_id, occurred_at, id);

CREATE FUNCTION scheduler_write_audit(
    p_tenant text, p_operation text, p_target text, p_before jsonb, p_after jsonb
) RETURNS void LANGUAGE sql SET search_path FROM CURRENT AS $$
    INSERT INTO scheduler_audit (tenant_id, actor, request_id, operation, target_id,
                                 reason_code, before_state, after_state)
    VALUES (p_tenant, COALESCE(NULLIF(current_setting('scheduler.actor', true), ''), 'scheduler-system'),
        NULLIF(current_setting('scheduler.request_id', true), ''), p_operation, p_target,
        COALESCE(NULLIF(current_setting('scheduler.reason_code', true), ''), p_operation), p_before, p_after)
$$;

CREATE FUNCTION scheduler_audit_schedule() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    op text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM scheduler_write_audit(NEW.tenant_id, 'schedule.create', NEW.id, '{}'::jsonb,
            jsonb_build_object('version', NEW.version, 'paused', NEW.paused));
    ELSIF OLD.version <> NEW.version THEN
        op := CASE WHEN OLD.paused = NEW.paused THEN 'schedule.replace'
                   WHEN NEW.paused THEN 'schedule.pause' ELSE 'schedule.resume' END;
        PERFORM scheduler_write_audit(NEW.tenant_id, op, NEW.id,
            jsonb_build_object('version', OLD.version, 'paused', OLD.paused),
            jsonb_build_object('version', NEW.version, 'paused', NEW.paused));
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER scheduler_audit_schedule AFTER INSERT OR UPDATE OF version ON schedules
    FOR EACH ROW EXECUTE FUNCTION scheduler_audit_schedule();

CREATE FUNCTION scheduler_audit_run() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF TG_OP = 'INSERT' AND NEW.request_key IS NOT NULL THEN
        PERFORM scheduler_write_audit(NEW.tenant_id,
            CASE WHEN NEW.schedule_id IS NULL THEN 'run.submit' ELSE 'run.trigger' END, NEW.id, '{}'::jsonb,
            jsonb_build_object('status', 'queued', 'schedule_id', NEW.schedule_id, 'schedule_version', NEW.schedule_version));
    ELSIF TG_OP = 'UPDATE' AND OLD.dead_letter_replays <> NEW.dead_letter_replays THEN
        PERFORM scheduler_write_audit(NEW.tenant_id, 'run.replay', NEW.id,
            jsonb_build_object('status', 'dead', 'replays', OLD.dead_letter_replays),
            jsonb_build_object('status', 'queued', 'replays', NEW.dead_letter_replays));
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER scheduler_audit_run AFTER INSERT OR UPDATE OF dead_letter_replays ON scheduled_runs
    FOR EACH ROW EXECUTE FUNCTION scheduler_audit_run();

CREATE FUNCTION scheduler_audit_cancel() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    PERFORM scheduler_write_audit(NEW.tenant_id, 'run.cancel', NEW.id,
        jsonb_build_object('status', OLD.status, 'attempt', OLD.attempts),
        jsonb_build_object('status', NEW.status, 'attempt', NEW.attempts));
    RETURN NEW;
END;
$$;

CREATE TRIGGER scheduler_audit_cancel AFTER UPDATE OF status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status <> NEW.status AND NEW.status = 'canceled')
    EXECUTE FUNCTION scheduler_audit_cancel();
