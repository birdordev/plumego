ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    DROP CONSTRAINT cmp_instance_operation_target,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
        OR (resource_kind='network' AND action='create-network')
        OR (resource_kind='subnet' AND action='create-subnet')
        OR (resource_kind='security-group' AND action='create-security-group')
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot'))
        OR (resource_kind='volume' AND action='restore-snapshot')
    ),
    ADD CONSTRAINT cmp_instance_operation_target CHECK (
        (scope_id IS NOT NULL AND external_id IS NOT NULL)
        OR (action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip','create-snapshot','restore-snapshot')
            AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
    );

ALTER TABLE cmp_network_intents
    ADD COLUMN restore_snapshot_scope_id bigint,
    ADD COLUMN restore_snapshot_id text,
    ADD COLUMN restore_disk_type text NOT NULL DEFAULT '',
    ADD COLUMN restore_size_gib int NOT NULL DEFAULT 0,
    ADD COLUMN restore_encrypted bool NOT NULL DEFAULT false,
    ADD FOREIGN KEY (restore_snapshot_scope_id,restore_snapshot_id) REFERENCES cmp_inventory_objects,
    ADD CHECK (
        (restore_snapshot_scope_id IS NULL AND restore_snapshot_id IS NULL AND restore_disk_type='' AND restore_size_gib=0 AND NOT restore_encrypted)
        OR (restore_snapshot_scope_id IS NOT NULL AND restore_snapshot_id IS NOT NULL AND restore_disk_type<>''
            AND restore_size_gib BETWEEN 10 AND 32768 AND parent_scope_id IS NOT NULL AND snapshot_class<>'' AND zone<>'')
    );

DROP INDEX cmp_resource_creation_asset;
CREATE UNIQUE INDEX cmp_resource_creation_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip','create-snapshot','restore-snapshot')
      AND scope_id IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='cmp_operation_event_outbox'::regclass AND conname='cmp_event_outbox_source') THEN
        ALTER TABLE cmp_operation_event_outbox
            DROP CONSTRAINT cmp_event_outbox_source,
            ADD CONSTRAINT cmp_event_outbox_source CHECK (
                (sequence IS NOT NULL AND alert_id IS NULL AND event_type ~
                    '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume)\.operation\.(succeeded|failed|needs_review)$') OR
                (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved')));
    ELSE
        ALTER TABLE cmp_operation_event_outbox
            DROP CONSTRAINT cmp_operation_event_outbox_event_type_check,
            ADD CONSTRAINT cmp_operation_event_outbox_event_type_check CHECK (
                event_type ~ '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume)\.operation\.(succeeded|failed|needs_review)$');
    END IF;
END;
$$;
