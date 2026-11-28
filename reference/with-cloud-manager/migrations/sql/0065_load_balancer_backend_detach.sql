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
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend'))
    );

CREATE TABLE cmp_load_balancer_backend_detach_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    product text NOT NULL CHECK (product IN ('tencent-clb','alibaba-alb','alibaba-nlb','huawei-elb')),
    target_group_id text NOT NULL CHECK (octet_length(target_group_id)<=512 AND target_group_id ~ '^[A-Za-z0-9_.-]+$'),
    member_key text NOT NULL CHECK (member_key ~ '^[0-9a-f]{32}$'),
    binding_key text NOT NULL DEFAULT '' CHECK (binding_key='' OR binding_key ~ '^[0-9a-f]{32}$'),
    absence_observations smallint NOT NULL DEFAULT 0 CHECK (absence_observations BETWEEN 0 AND 2),
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0)),
    CHECK (absence_observations=0 OR binding_key<>'')
);
CREATE INDEX cmp_load_balancer_backend_detach_target ON cmp_load_balancer_backend_detach_intents(target_group_id,operation_id);
