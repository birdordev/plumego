ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_operation_kind_action;
ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operation_target;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_operation_kind_action CHECK (
    (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
    OR (resource_kind='network' AND action='create-network')
    OR (resource_kind='subnet' AND action='create-subnet')
    OR (resource_kind='security-group' AND action='create-security-group')
    OR (resource_kind='security-group-rule' AND action='create-security-group-rule')
);
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operation_target CHECK (
    (scope_id IS NOT NULL AND external_id IS NOT NULL)
    OR (action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule')
        AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
);
DROP INDEX cmp_resource_creation_asset;
CREATE UNIQUE INDEX cmp_resource_creation_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule') AND scope_id IS NOT NULL;

ALTER TABLE cmp_network_intents ADD COLUMN rule_spec jsonb;
ALTER TABLE cmp_network_intents ADD CONSTRAINT cmp_network_intent_rule_shape CHECK (
    rule_spec IS NULL OR (jsonb_typeof(rule_spec)='object' AND cidr IS NULL AND name='' AND parent_scope_id IS NOT NULL)
);

ALTER TABLE cmp_instance_operation_approvals ADD COLUMN reviewer_membership_revision bigint
    CHECK (reviewer_membership_revision > 0);

ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT cmp_operation_event_outbox_event_type_check;
ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT cmp_operation_event_outbox_event_type_check
    CHECK (event_type ~ '^(instance|network|subnet|security-group|security-group-rule)\.operation\.(succeeded|failed|needs_review)$');
