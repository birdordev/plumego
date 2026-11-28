ALTER TABLE cmp_inventory_scopes
    ADD CONSTRAINT cmp_inventory_dns_zone_scope CHECK (kind<>'dns-zone' OR region='global');

CREATE TABLE cmp_dns_zone_records (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    zone_name text NOT NULL,
    credential_revision bigint NOT NULL,
    refresh_started_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    records jsonb NOT NULL CHECK (jsonb_typeof(records)='array' AND jsonb_array_length(records)<=5000),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
