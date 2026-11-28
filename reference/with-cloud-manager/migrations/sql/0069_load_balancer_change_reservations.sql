CREATE TABLE cmp_load_balancer_change_reservations (
    operation_id text PRIMARY KEY,
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    source_scope_id bigint,
    source_instance_id text,
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects,
    FOREIGN KEY (source_scope_id,source_instance_id) REFERENCES cmp_inventory_objects MATCH FULL
);

CREATE INDEX cmp_load_balancer_change_target ON cmp_load_balancer_change_reservations(scope_id,external_id);
CREATE INDEX cmp_load_balancer_change_source ON cmp_load_balancer_change_reservations(source_scope_id,source_instance_id)
    WHERE source_scope_id IS NOT NULL;
