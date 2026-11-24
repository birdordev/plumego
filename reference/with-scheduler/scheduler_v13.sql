CREATE FUNCTION scheduler_notify_queue() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    PERFORM pg_notify('plumego_mq_' || md5(current_schema()), '');
    RETURN NEW;
END
$$;

CREATE TRIGGER scheduler_notify_enqueued AFTER INSERT ON mq_tasks
    FOR EACH ROW WHEN (NEW.status = 'queued') EXECUTE FUNCTION scheduler_notify_queue();

CREATE TRIGGER scheduler_notify_rescheduled AFTER UPDATE OF status, available_at ON mq_tasks
    FOR EACH ROW WHEN (NEW.status = 'queued' AND (OLD.status <> NEW.status OR OLD.available_at <> NEW.available_at))
    EXECUTE FUNCTION scheduler_notify_queue();
