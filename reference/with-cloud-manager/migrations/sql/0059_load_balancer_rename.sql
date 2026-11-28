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
        OR (resource_kind='load-balancer' AND action='rename-load-balancer')
    );

CREATE TABLE cmp_load_balancer_rename_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    product text NOT NULL CHECK (product IN ('tencent-clb','alibaba-alb','alibaba-nlb','alibaba-clb','huawei-elb')),
    current_name text NOT NULL CHECK (octet_length(current_name)<=1024),
    name text NOT NULL CHECK (name ~ '^[A-Za-z][A-Za-z0-9_-]{1,59}$' AND name<>current_name),
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0)
        OR (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0))
);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='cmp_operation_event_outbox'::regclass AND conname='cmp_event_outbox_source') THEN
        ALTER TABLE cmp_operation_event_outbox
            DROP CONSTRAINT cmp_event_outbox_source,
            ADD CONSTRAINT cmp_event_outbox_source CHECK (
                (sequence IS NOT NULL AND alert_id IS NULL AND event_type ~
                    '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume|image|load-balancer)\.operation\.(succeeded|failed|needs_review)$') OR
                (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved')));
    ELSE
        ALTER TABLE cmp_operation_event_outbox
            DROP CONSTRAINT cmp_operation_event_outbox_event_type_check,
            ADD CONSTRAINT cmp_operation_event_outbox_event_type_check CHECK (
                event_type ~ '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume|image|load-balancer)\.operation\.(succeeded|failed|needs_review)$');
    END IF;
END;
$$;
