CREATE TABLE cmp_image_copy_reservations (
    scope_id bigint NOT NULL,
    image_id text NOT NULL,
    operation_id text NOT NULL UNIQUE,
    PRIMARY KEY (scope_id,image_id),
    FOREIGN KEY (scope_id,image_id) REFERENCES cmp_inventory_objects
);
