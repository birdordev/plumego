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
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','delete-image'))
        OR (resource_kind='volume' AND action='restore-snapshot')
    ),
    ADD CONSTRAINT cmp_instance_operation_target CHECK (
        (scope_id IS NOT NULL AND external_id IS NOT NULL)
        OR (action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip','create-snapshot','restore-snapshot','create-image')
            AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
    );


CREATE UNIQUE INDEX cmp_created_image_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action='create-image' AND scope_id IS NOT NULL;

ALTER TABLE cmp_instance_image_reservations
    ADD FOREIGN KEY (operation_id) REFERENCES cmp_instance_operations;
