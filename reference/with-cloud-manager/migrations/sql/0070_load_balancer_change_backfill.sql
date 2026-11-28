ALTER TABLE cmp_load_balancer_change_reservations ADD CONSTRAINT cmp_load_balancer_change_operation_fk
    FOREIGN KEY (operation_id) REFERENCES cmp_instance_operations;

CREATE FUNCTION cmp_update_load_balancer_change_reservation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('awaiting_approval','queued','applying','observing','needs_review') THEN
        INSERT INTO cmp_load_balancer_change_reservations(operation_id,scope_id,external_id,source_scope_id,source_instance_id)
        SELECT NEW.operation_id,NEW.scope_id,NEW.external_id,i.instance_scope_id,i.instance_id
        FROM (VALUES (NEW.operation_id)) AS active(operation_id)
        LEFT JOIN cmp_load_balancer_backend_attach_intents AS i USING (operation_id)
        ON CONFLICT (operation_id) DO NOTHING;
    ELSE
        DELETE FROM cmp_load_balancer_change_reservations WHERE operation_id=NEW.operation_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_load_balancer_change_reservation
    AFTER INSERT OR UPDATE OF status ON cmp_instance_operations
    FOR EACH ROW WHEN (NEW.resource_kind='load-balancer')
    EXECUTE FUNCTION cmp_update_load_balancer_change_reservation();

CREATE FUNCTION cmp_set_load_balancer_change_source() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE cmp_load_balancer_change_reservations
    SET source_scope_id=NEW.instance_scope_id,source_instance_id=NEW.instance_id WHERE operation_id=NEW.operation_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_load_balancer_change_source
    AFTER INSERT ON cmp_load_balancer_backend_attach_intents
    FOR EACH ROW EXECUTE FUNCTION cmp_set_load_balancer_change_source();

INSERT INTO cmp_load_balancer_change_reservations(operation_id,scope_id,external_id,source_scope_id,source_instance_id)
SELECT o.operation_id,o.scope_id,o.external_id,i.instance_scope_id,i.instance_id
FROM cmp_instance_operations AS o LEFT JOIN cmp_load_balancer_backend_attach_intents AS i USING (operation_id)
WHERE o.resource_kind='load-balancer' AND o.status IN ('awaiting_approval','queued','applying','observing','needs_review');
