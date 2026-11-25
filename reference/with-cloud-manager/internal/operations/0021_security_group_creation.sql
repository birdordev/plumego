ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_operation_kind_action;
ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operation_target;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_operation_kind_action CHECK (
    (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
    OR (resource_kind='network' AND action='create-network')
    OR (resource_kind='subnet' AND action='create-subnet')
    OR (resource_kind='security-group' AND action='create-security-group')
);
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operation_target CHECK (
    (scope_id IS NOT NULL AND external_id IS NOT NULL)
    OR (action IN ('create','create-network','create-subnet','create-security-group')
        AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
);
DROP INDEX cmp_resource_creation_asset;
CREATE UNIQUE INDEX cmp_resource_creation_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action IN ('create','create-network','create-subnet','create-security-group') AND scope_id IS NOT NULL;

ALTER TABLE cmp_instance_approval_rules DROP CONSTRAINT cmp_instance_approval_rules_action_check;
ALTER TABLE cmp_instance_approval_rules ADD CONSTRAINT cmp_instance_approval_rules_action_check
    CHECK (action IN ('start','stop','reboot','create','attach-volume','create-network','create-subnet','create-security-group'));
ALTER TABLE cmp_network_intents DROP CONSTRAINT cmp_network_intents_check;
ALTER TABLE cmp_network_intents ADD CONSTRAINT cmp_network_intent_parent_coordinates CHECK (
    (parent_scope_id IS NULL AND parent_id IS NULL AND parent_revision=0) OR
    (parent_scope_id IS NOT NULL AND parent_id IS NOT NULL AND parent_revision>0)
);

ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT cmp_operation_event_outbox_event_type_check;
ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT cmp_operation_event_outbox_event_type_check
    CHECK (event_type ~ '^(instance|network|subnet|security-group)\.operation\.(succeeded|failed|needs_review)$');
