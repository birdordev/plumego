ALTER TABLE cmp_network_intents
    ADD COLUMN bandwidth_mbps smallint NOT NULL DEFAULT 0 CHECK (bandwidth_mbps=0 OR bandwidth_mbps BETWEEN 5 AND 200),
    ADD COLUMN address_pool text NOT NULL DEFAULT '' CHECK (
        address_pool='' OR (bandwidth_mbps>0 AND address_pool ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$'));

ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    DROP CONSTRAINT cmp_instance_operation_target,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
        OR (resource_kind='network' AND action='create-network')
        OR (resource_kind='subnet' AND action='create-subnet')
        OR (resource_kind='security-group' AND action='create-security-group')
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action='create-elastic-ip')
    ),
    ADD CONSTRAINT cmp_instance_operation_target CHECK (
        (scope_id IS NOT NULL AND external_id IS NOT NULL)
        OR (action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip')
            AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
    );

DROP INDEX cmp_resource_creation_asset;
CREATE UNIQUE INDEX cmp_resource_creation_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip')
      AND scope_id IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='cmp_operation_event_outbox'::regclass AND conname='cmp_event_outbox_source') THEN
        ALTER TABLE cmp_operation_event_outbox
            DROP CONSTRAINT cmp_event_outbox_source,
            ADD CONSTRAINT cmp_event_outbox_source CHECK (
                (sequence IS NOT NULL AND alert_id IS NULL AND event_type ~
                    '^(instance|network|subnet|security-group|security-group-rule|elastic-ip)\.operation\.(succeeded|failed|needs_review)$') OR
                (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved')));
    ELSE
        ALTER TABLE cmp_operation_event_outbox
            DROP CONSTRAINT cmp_operation_event_outbox_event_type_check,
            ADD CONSTRAINT cmp_operation_event_outbox_event_type_check CHECK (
                event_type ~ '^(instance|network|subnet|security-group|security-group-rule|elastic-ip)\.operation\.(succeeded|failed|needs_review)$');
    END IF;
END;
$$;
