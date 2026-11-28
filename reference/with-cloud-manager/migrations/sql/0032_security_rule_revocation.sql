ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_operation_kind_action;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_operation_kind_action CHECK (
    (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
    OR (resource_kind='network' AND action='create-network')
    OR (resource_kind='subnet' AND action='create-subnet')
    OR (resource_kind='security-group' AND action='create-security-group')
    OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
);

ALTER TABLE cmp_network_intents ADD COLUMN absence_observations smallint NOT NULL DEFAULT 0
    CHECK (absence_observations BETWEEN 0 AND 2);
