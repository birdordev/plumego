ALTER TABLE cmp_dns_zone_records ADD COLUMN invalidated boolean NOT NULL DEFAULT false;

CREATE TABLE cmp_dns_zone_change_reservations (
    operation_id text PRIMARY KEY,
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    UNIQUE (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects
);
