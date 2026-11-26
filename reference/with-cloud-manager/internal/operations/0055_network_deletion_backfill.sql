ALTER TABLE cmp_network_deletion_reservations ADD CONSTRAINT cmp_network_deletion_operation_fk
    FOREIGN KEY (operation_id) REFERENCES cmp_instance_operations;

CREATE FUNCTION cmp_update_network_deletion_reservation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('awaiting_approval','queued','applying','observing','needs_review') THEN
        INSERT INTO cmp_network_deletion_reservations(scope_id,external_id,operation_id)
        VALUES (NEW.scope_id,NEW.external_id,NEW.operation_id)
        ON CONFLICT (operation_id) DO NOTHING;
    ELSE
        DELETE FROM cmp_network_deletion_reservations WHERE operation_id=NEW.operation_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_network_deletion_reservation
    AFTER INSERT OR UPDATE OF status ON cmp_instance_operations
    FOR EACH ROW
    WHEN (NEW.action IN ('delete-network','delete-subnet','delete-security-group'))
    EXECUTE FUNCTION cmp_update_network_deletion_reservation();

-- 先安装触发器，同一迁移事务再回填旧版本已受理的操作。
INSERT INTO cmp_network_deletion_reservations(scope_id,external_id,operation_id)
SELECT scope_id,external_id,operation_id FROM cmp_instance_operations
WHERE action IN ('delete-network','delete-subnet','delete-security-group')
  AND status IN ('awaiting_approval','queued','applying','observing','needs_review');
