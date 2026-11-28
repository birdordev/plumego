CREATE TABLE cmp_inventory_schedules (
    schedule_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('region','zone','instance','volume','network','subnet','security-group','image')),
    interval_seconds integer NOT NULL CHECK (interval_seconds BETWEEN 60 AND 604800),
    enabled boolean NOT NULL DEFAULT true,
    next_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    lease_until timestamptz,
    fence bigint NOT NULL DEFAULT 0,
    failures integer NOT NULL DEFAULT 0 CHECK (failures BETWEEN 0 AND 16),
    last_outcome text CHECK (last_outcome IN ('success','failure','throttled')),
    last_finished_at timestamptz,
    CHECK ((region='')=(kind='region')),
    UNIQUE (tenant_id,account_id,region,kind),
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts
);

CREATE INDEX cmp_inventory_schedule_due ON cmp_inventory_schedules (next_at,schedule_id) WHERE enabled;
