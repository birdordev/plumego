ALTER TABLE cmp_inventory_schedules
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    DROP CONSTRAINT cmp_inventory_schedules_target_check,
    ADD COLUMN last_success_at timestamptz,
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule',
                 'elastic-ip','snapshot','load-balancer','dns-zone','database-instance','object-bucket','cache-instance',
                 'kubernetes-cluster','kubernetes-node-pools','kubernetes-workloads')),
    ADD CONSTRAINT cmp_inventory_schedules_target_check CHECK (
        CASE WHEN kind IN ('kubernetes-node-pools','kubernetes-workloads') THEN target_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$'
             ELSE target_id='' END);

UPDATE cmp_inventory_schedules SET last_success_at=last_finished_at
WHERE target_id<>'' AND last_outcome='success';
