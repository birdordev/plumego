CREATE SEQUENCE cmp_inventory_dispatch_turn AS bigint;

CREATE TABLE cmp_inventory_tenant_dispatch (
    tenant_id text PRIMARY KEY REFERENCES cmp_tenants ON DELETE CASCADE,
    last_turn bigint NOT NULL CHECK (last_turn > 0)
);
