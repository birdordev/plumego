ALTER TABLE cmp_inventory_objects ADD COLUMN workload_change_generation bigint NOT NULL DEFAULT 0;

CREATE TABLE cmp_workload_change_reservations (
    operation_id text PRIMARY KEY,
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    UNIQUE (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects
);
