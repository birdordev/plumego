ALTER TABLE schedules ADD COLUMN claim_paused boolean NOT NULL DEFAULT false;

CREATE INDEX schedules_claim_paused ON schedules (tenant_id, id) WHERE claim_paused;
CREATE INDEX scheduled_runs_schedule_members ON scheduled_runs (tenant_id, schedule_id, id) WHERE schedule_id IS NOT NULL;

CREATE VIEW scheduler_blocked_claims AS
SELECT r.id AS task_id
FROM schedules s JOIN scheduled_runs r ON r.tenant_id = s.tenant_id AND r.schedule_id = s.id
WHERE s.claim_paused;

CREATE OR REPLACE FUNCTION scheduler_audit_schedule() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    op text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM scheduler_write_audit(NEW.tenant_id, 'schedule.create', NEW.id, '{}'::jsonb,
            jsonb_build_object('version', NEW.version, 'paused', NEW.paused, 'claim_paused', NEW.claim_paused));
    ELSIF OLD.version <> NEW.version THEN
        op := CASE WHEN OLD.paused <> NEW.paused THEN
                       CASE WHEN NEW.paused THEN 'schedule.pause' ELSE 'schedule.resume' END
                   WHEN OLD.claim_paused <> NEW.claim_paused THEN
                       CASE WHEN NEW.claim_paused THEN 'schedule.pause_claims' ELSE 'schedule.resume_claims' END
                   WHEN current_setting('scheduler.cancel_running', true) = 'true' THEN 'schedule.cancel_running'
                   ELSE 'schedule.replace' END;
        PERFORM scheduler_write_audit(NEW.tenant_id, op, NEW.id,
            jsonb_build_object('version', OLD.version, 'paused', OLD.paused, 'claim_paused', OLD.claim_paused),
            jsonb_build_object('version', NEW.version, 'paused', NEW.paused, 'claim_paused', NEW.claim_paused));
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER scheduler_notify_claim_resume AFTER UPDATE OF claim_paused ON schedules
    FOR EACH ROW WHEN (OLD.claim_paused AND NOT NEW.claim_paused)
    EXECUTE FUNCTION scheduler_notify_queue();
