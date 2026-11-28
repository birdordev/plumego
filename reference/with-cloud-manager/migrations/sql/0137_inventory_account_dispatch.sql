CREATE TABLE cmp_inventory_account_dispatch (
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    last_claimed_at timestamptz NOT NULL,
    cooldown_until timestamptz,
    schedule_id bigint,
    schedule_fence bigint,
    lease_until timestamptz,
    PRIMARY KEY (tenant_id,account_id),
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts ON DELETE CASCADE,
    CHECK (num_nonnulls(schedule_id,schedule_fence,lease_until) IN (0,3))
);

CREATE INDEX cmp_inventory_schedule_account_due ON cmp_inventory_schedules
    (tenant_id,account_id,next_at,schedule_id)
WHERE enabled AND kind NOT IN ('kubernetes-workload-watch','instance-idle');
