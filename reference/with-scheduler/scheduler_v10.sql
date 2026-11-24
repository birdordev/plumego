CREATE FUNCTION scheduler_track_attempt() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF NEW.status = 'leased' AND NEW.attempts > OLD.attempts THEN
        IF OLD.status = 'leased' THEN
            UPDATE mq_task_attempts
            SET finished_at = NEW.updated_at, reason = 'worker lease expired', success = FALSE
            WHERE task_id = OLD.id AND attempt_no = OLD.attempts AND finished_at IS NULL;
        END IF;
        INSERT INTO mq_task_attempts (task_id, attempt_no, started_at)
        VALUES (NEW.id, NEW.attempts, NEW.updated_at);
    ELSIF OLD.status = 'leased' AND NEW.status <> 'leased' THEN
        UPDATE mq_task_attempts SET finished_at = NEW.updated_at, success = NEW.status = 'done',
            reason = CASE NEW.status
                WHEN 'done' THEN NULL
                WHEN 'canceled' THEN 'task canceled'
                WHEN 'expired' THEN 'task age limit exceeded'
                ELSE NEW.last_error
            END
        WHERE task_id = OLD.id AND attempt_no = OLD.attempts AND finished_at IS NULL;
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER scheduler_track_attempt AFTER UPDATE OF status, attempts ON mq_tasks
    FOR EACH ROW EXECUTE FUNCTION scheduler_track_attempt();
