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
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    );

CREATE TABLE cmp_dns_record_creation_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    zone_name text NOT NULL,
    spec jsonb NOT NULL CHECK (jsonb_typeof(spec)='object' AND spec->>'type' IN ('A','AAAA')),
    recovery_token text NOT NULL UNIQUE CHECK (recovery_token ~ '^[A-Z2-7]{26}$'),
    record_id text NOT NULL DEFAULT '' CHECK (record_id='' OR record_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'),
    reviewed_record_id text NOT NULL DEFAULT '' CHECK (reviewed_record_id='' OR reviewed_record_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'),
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0))
);

ALTER TABLE cmp_instance_observation_retries ADD COLUMN provider_resource_id text NOT NULL DEFAULT ''
    CHECK (provider_resource_id='' OR provider_resource_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$');
