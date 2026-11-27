SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_operation_tasks ADD COLUMN first_lease_at timestamptz;

CREATE FUNCTION cmp_record_operation_first_lease() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.first_lease_at IS NULL AND OLD.status = 'queued' AND OLD.attempts = 0
       AND NEW.topic = 'cmp.instance.v1' AND NEW.status = 'leased' AND NEW.attempts = 1
       AND EXISTS (SELECT 1 FROM cmp_instance_operations AS o
                   WHERE (o.operation_id,o.tenant_id)=(NEW.id,NEW.tenant_id) AND o.status='queued') THEN
        NEW.first_lease_at := clock_timestamp();
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_operation_first_lease BEFORE UPDATE OF status ON cmp_operation_tasks
    FOR EACH ROW EXECUTE FUNCTION cmp_record_operation_first_lease();
