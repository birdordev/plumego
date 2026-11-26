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
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action='restore-snapshot')
    ),
    ADD CONSTRAINT cmp_instance_operation_target CHECK (
        (scope_id IS NOT NULL AND external_id IS NOT NULL)
        OR (action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip','create-snapshot','restore-snapshot','create-image','copy-image')
            AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
    );



DROP INDEX cmp_created_image_asset;
CREATE UNIQUE INDEX cmp_created_image_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action IN ('create-image','copy-image') AND scope_id IS NOT NULL;

ALTER TABLE cmp_image_copy_reservations ADD FOREIGN KEY (operation_id) REFERENCES cmp_instance_operations;

CREATE TABLE cmp_image_copy_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_network_intents,
    tenant_id text NOT NULL,
    source_account text NOT NULL,
    source_region text NOT NULL,
    source_manual_ownership boolean NOT NULL,
    source_credential_revision bigint NOT NULL CHECK (source_credential_revision>0),
    observation_credential_revision bigint NOT NULL DEFAULT 0 CHECK (observation_credential_revision>=0),
    FOREIGN KEY (tenant_id,source_account) REFERENCES cmp_cloud_accounts
);
