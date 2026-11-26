ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_operation_kind_action;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_operation_kind_action CHECK (
    (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
    OR (resource_kind='network' AND action='create-network')
    OR (resource_kind='subnet' AND action='create-subnet')
    OR (resource_kind='security-group' AND action='create-security-group')
    OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
    OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
);

ALTER TABLE cmp_network_intents ADD COLUMN binding_address inet CHECK (
    binding_address IS NULL OR (family(binding_address)=4 AND masklen(binding_address)=32
        AND parent_scope_id IS NOT NULL AND release_address IS NULL));
ALTER TABLE cmp_network_intents ADD COLUMN elastic_ip_port text NOT NULL DEFAULT ''
    CHECK (elastic_ip_port='' OR (binding_address IS NOT NULL
        AND elastic_ip_port ~ '^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$'));
ALTER TABLE cmp_network_intents ADD COLUMN binding_observations smallint NOT NULL DEFAULT 0
    CHECK (binding_observations BETWEEN 0 AND 2);

CREATE INDEX cmp_elastic_ip_binding_instance ON cmp_network_intents (parent_scope_id,parent_id)
    WHERE binding_address IS NOT NULL;
