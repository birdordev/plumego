ALTER TABLE cmp_inventory_schedules
    ADD COLUMN target_id text NOT NULL DEFAULT '',
    DROP CONSTRAINT cmp_inventory_schedules_tenant_id_account_id_region_kind_key,
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    ADD CONSTRAINT cmp_inventory_schedules_scope_target_key
        UNIQUE (tenant_id,account_id,region,kind,target_id),
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule',
                 'elastic-ip','snapshot','load-balancer','dns-zone','database-instance','object-bucket','cache-instance',
                 'kubernetes-cluster','kubernetes-node-pools')),
    ADD CONSTRAINT cmp_inventory_schedules_target_check CHECK (
        CASE WHEN kind='kubernetes-node-pools' THEN target_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'
             ELSE target_id='' END);
