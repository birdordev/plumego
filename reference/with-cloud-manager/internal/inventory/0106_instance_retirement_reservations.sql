CREATE TABLE cmp_instance_retirement_reservations (
    operation_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    instance_scope bigint NOT NULL,
    instance_id text NOT NULL,
    disk_scope bigint NOT NULL,
    disk_id text NOT NULL,
    UNIQUE (instance_scope,instance_id),
    UNIQUE (disk_scope,disk_id),
    FOREIGN KEY (tenant_id,instance_scope) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (tenant_id,disk_scope) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (instance_scope,instance_id) REFERENCES cmp_inventory_objects,
    FOREIGN KEY (disk_scope,disk_id) REFERENCES cmp_inventory_objects
);
