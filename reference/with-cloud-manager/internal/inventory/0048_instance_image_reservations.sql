CREATE TABLE cmp_instance_image_reservations (
    scope_id bigint NOT NULL,
    instance_id text NOT NULL,
    operation_id text NOT NULL UNIQUE,
    PRIMARY KEY (scope_id,instance_id),
    FOREIGN KEY (scope_id,instance_id) REFERENCES cmp_inventory_objects
);
