ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
        OR (resource_kind='network' AND action IN ('create-network','delete-network'))
        OR (resource_kind='subnet' AND action IN ('create-subnet','delete-subnet'))
        OR (resource_kind='security-group' AND action='create-security-group')
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action='restore-snapshot')
    );

CREATE TABLE cmp_network_deletion_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_network_intents,
    parent_scope_id bigint,
    parent_id text,
    parent_revision bigint NOT NULL,
    FOREIGN KEY (parent_scope_id,parent_id) REFERENCES cmp_inventory_objects,
    CHECK ((parent_scope_id IS NULL AND parent_id IS NULL AND parent_revision=0)
        OR (parent_scope_id IS NOT NULL AND parent_id IS NOT NULL AND parent_revision>0))
);
CREATE INDEX cmp_network_deletion_parent ON cmp_network_deletion_intents (parent_scope_id,parent_id)
    WHERE parent_scope_id IS NOT NULL;
CREATE INDEX cmp_inventory_live_incoming_links ON cmp_inventory_objects USING gin (links jsonb_path_ops)
    WHERE removed_at IS NULL;
