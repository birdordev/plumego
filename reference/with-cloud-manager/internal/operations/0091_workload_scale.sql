ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
        OR (resource_kind='network' AND action IN ('create-network','delete-network'))
        OR (resource_kind='subnet' AND action IN ('create-subnet','delete-subnet'))
        OR (resource_kind='security-group' AND action IN ('create-security-group','delete-security-group'))
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action='restore-snapshot')
        OR (resource_kind='kubernetes-cluster' AND action='scale-workload')
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status','delete-dns-record'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    );

CREATE TABLE cmp_workload_scale_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    uid text NOT NULL,
    namespace text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('Deployment','StatefulSet')),
    name text NOT NULL,
    current_replicas integer NOT NULL CHECK (current_replicas BETWEEN 0 AND 1000000),
    replicas integer NOT NULL CHECK (replicas BETWEEN 1 AND 100 AND replicas<>current_replicas),
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0))
);

ALTER TABLE cmp_workload_change_reservations ADD CONSTRAINT cmp_workload_change_operation_fk
    FOREIGN KEY (operation_id) REFERENCES cmp_instance_operations;

CREATE FUNCTION cmp_update_workload_change_reservation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IN ('awaiting_approval','queued','applying','observing','needs_review') THEN
        INSERT INTO cmp_workload_change_reservations(operation_id,scope_id,external_id)
        VALUES (NEW.operation_id,NEW.scope_id,NEW.external_id)
        ON CONFLICT (operation_id) DO NOTHING;
    ELSE
        DELETE FROM cmp_workload_change_reservations WHERE operation_id=NEW.operation_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_workload_change_reservation
    AFTER INSERT OR UPDATE OF status ON cmp_instance_operations
    FOR EACH ROW WHEN (NEW.resource_kind='kubernetes-cluster')
    EXECUTE FUNCTION cmp_update_workload_change_reservation();
