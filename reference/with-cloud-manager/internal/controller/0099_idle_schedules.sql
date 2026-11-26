ALTER TABLE cmp_inventory_schedules
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    DROP CONSTRAINT cmp_inventory_schedules_target_check,
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule',
                 'elastic-ip','snapshot','load-balancer','dns-zone','database-instance','object-bucket','cache-instance',
                 'kubernetes-cluster','kubernetes-node-pools','kubernetes-workloads','kubernetes-workload-watch','instance-idle')),
    ADD CONSTRAINT cmp_inventory_schedules_target_check CHECK (
        CASE WHEN kind IN ('kubernetes-node-pools','kubernetes-workloads','kubernetes-workload-watch','instance-idle')
             THEN target_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$' ELSE target_id='' END),
    ADD CONSTRAINT cmp_inventory_schedules_idle_interval_check CHECK (
        kind<>'instance-idle' OR interval_seconds BETWEEN 3600 AND 86400);

DROP INDEX cmp_inventory_schedule_due;

CREATE INDEX cmp_inventory_schedule_due ON cmp_inventory_schedules (next_at,schedule_id)
WHERE enabled AND kind NOT IN ('kubernetes-workload-watch','instance-idle');

CREATE INDEX cmp_inventory_schedule_idle_due ON cmp_inventory_schedules (next_at,schedule_id)
WHERE enabled AND kind='instance-idle';
