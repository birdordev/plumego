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
        OR (resource_kind='database-instance' AND action='rename-database-instance')
        OR (resource_kind='kubernetes-cluster' AND action='scale-workload')
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status','delete-dns-record'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    ) NOT VALID;

CREATE TABLE cmp_instance_retirement_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    system_disk_id text NOT NULL,
    disk_ownership_revision bigint NOT NULL CHECK (disk_ownership_revision>0),
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    absence_observations smallint NOT NULL DEFAULT 0 CHECK (absence_observations BETWEEN 0 AND 2),
    last_absence_attempt integer NOT NULL DEFAULT 0 CHECK (last_absence_attempt>=0),
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0))
);

ALTER TABLE cmp_instance_retirement_reservations ADD CONSTRAINT cmp_instance_retirement_operation_fk
    FOREIGN KEY (tenant_id,project_id,operation_id) REFERENCES cmp_instance_operations (tenant_id,project_id,operation_id);

CREATE FUNCTION cmp_release_instance_retirement_reservation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM cmp_instance_retirement_reservations WHERE operation_id=NEW.operation_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_instance_retirement_release
    AFTER UPDATE OF status ON cmp_instance_operations FOR EACH ROW
    WHEN (NEW.action='retire-instance' AND NEW.status IN ('succeeded','failed'))
    EXECUTE FUNCTION cmp_release_instance_retirement_reservation();
