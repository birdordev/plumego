CREATE TABLE cmp_load_balancer_backend_keys (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    secret bytea NOT NULL CHECK (octet_length(secret)=32),
    change_observed_at timestamptz NOT NULL DEFAULT '-infinity',
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
