CREATE TABLE cmp_network_deletion_reservations (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    operation_id text NOT NULL UNIQUE,
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects
);
