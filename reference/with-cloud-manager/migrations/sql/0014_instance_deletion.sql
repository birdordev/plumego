ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operations_action_check;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operations_action_check
    CHECK (action IN ('start','stop','reboot','delete'));

CREATE TABLE cmp_protected_instances (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects
);
