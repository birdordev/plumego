ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume','retire-instance'))
        OR (resource_kind='network' AND action IN ('create-network','delete-network'))
        OR (resource_kind='subnet' AND action IN ('create-subnet','delete-subnet'))
        OR (resource_kind='security-group' AND action IN ('create-security-group','delete-security-group'))
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action='restore-snapshot')
        OR (resource_kind='cache-instance' AND action='rename-cache-instance')
        OR (resource_kind='database-instance' AND action='rename-database-instance')
        OR (resource_kind='kubernetes-cluster' AND action='scale-workload')
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status','delete-dns-record'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    ) NOT VALID;

CREATE TABLE cmp_cache_rename_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    product text NOT NULL CHECK (product IN ('tencent-redis','alibaba-tair','huawei-dcs')),
    current_name text NOT NULL CHECK (octet_length(current_name)<=1024),
    name text NOT NULL CHECK (name ~ '^[A-Za-z][A-Za-z0-9_-]{3,59}$' AND name<>current_name),
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0))
);

ALTER TABLE cmp_cache_rename_reservations ADD CONSTRAINT cmp_cache_rename_operation_fk
    FOREIGN KEY (operation_id) REFERENCES cmp_instance_operations;

CREATE FUNCTION cmp_update_cache_rename_reservation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('awaiting_approval','queued','applying','observing','needs_review') THEN
        INSERT INTO cmp_cache_rename_reservations(operation_id,scope_id,external_id)
        VALUES (NEW.operation_id,NEW.scope_id,NEW.external_id)
        ON CONFLICT (operation_id) DO NOTHING;
    ELSE
        DELETE FROM cmp_cache_rename_reservations WHERE operation_id=NEW.operation_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_cache_rename_reservation
    AFTER INSERT OR UPDATE OF status ON cmp_instance_operations
    FOR EACH ROW WHEN (NEW.action='rename-cache-instance')
    EXECUTE FUNCTION cmp_update_cache_rename_reservation();
