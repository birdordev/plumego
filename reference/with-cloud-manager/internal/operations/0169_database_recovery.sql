SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='bare-metal' AND action IN ('power-on-bare-metal','shutdown-bare-metal','stage-bare-metal-pxe','cancel-bare-metal-pxe'))
        OR (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume','retire-instance','resize-instance'))
        OR (resource_kind='object-bucket' AND action IN ('set-bucket-versioning','enable-bucket-encryption'))
        OR (resource_kind='network' AND action IN ('create-network','delete-network'))
        OR (resource_kind='subnet' AND action IN ('create-subnet','delete-subnet'))
        OR (resource_kind='security-group' AND action IN ('create-security-group','delete-security-group'))
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action IN ('restore-snapshot','resize-volume','create-volume','release-volume'))
        OR (resource_kind='cache-instance' AND action='rename-cache-instance')
        OR (resource_kind='database-instance' AND action IN ('rename-database-instance','recover-database-instance'))
        OR (resource_kind='kubernetes-cluster' AND action='scale-workload')
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status','update-dns-record','delete-dns-record'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    ) NOT VALID;

CREATE TABLE cmp_database_recovery_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    observed_at timestamptz NOT NULL,
    spec jsonb NOT NULL CHECK (jsonb_typeof(spec)='object'),
    dependency_revisions jsonb NOT NULL CHECK (jsonb_typeof(dependency_revisions)='array' AND jsonb_array_length(dependency_revisions)=3),
    provider_resource_id text NOT NULL DEFAULT '',
    registered_scope_id bigint,
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    CHECK (registered_scope_id IS NULL OR provider_resource_id<>''),
    FOREIGN KEY (registered_scope_id,provider_resource_id) REFERENCES cmp_inventory_objects(scope_id,external_id),
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0))
);

CREATE UNIQUE INDEX cmp_database_recovery_receipt ON cmp_database_recovery_intents(registered_scope_id,provider_resource_id)
    WHERE registered_scope_id IS NOT NULL;

DROP TRIGGER cmp_database_rename_reservation ON cmp_instance_operations;
CREATE TRIGGER cmp_database_rename_reservation
    AFTER INSERT OR UPDATE OF status ON cmp_instance_operations
    FOR EACH ROW WHEN (NEW.action IN ('rename-database-instance','recover-database-instance'))
    EXECUTE FUNCTION cmp_update_database_rename_reservation();
