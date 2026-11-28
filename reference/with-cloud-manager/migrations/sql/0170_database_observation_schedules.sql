ALTER TABLE cmp_inventory_schedules
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    DROP CONSTRAINT cmp_inventory_schedules_target_check,
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule',
                 'elastic-ip','snapshot','load-balancer','dns-zone','database-instance','object-bucket','cache-instance',
                 'kubernetes-cluster','kubernetes-node-pools','kubernetes-workloads','kubernetes-workload-watch','instance-idle',
                 'bare-metal','object-bucket-encryption','object-bucket-versioning','database-backups','database-restore-windows')),
    ADD CONSTRAINT cmp_inventory_schedules_target_check CHECK (
        CASE WHEN kind IN ('kubernetes-node-pools','kubernetes-workloads','kubernetes-workload-watch','instance-idle',
                          'database-backups','database-restore-windows')
             THEN target_id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$' ELSE target_id='' END),
    ADD CONSTRAINT cmp_inventory_schedules_database_observation_scope CHECK (
        kind NOT IN ('database-backups','database-restore-windows')
        OR (region<>'' AND region<>'global'));

DROP INDEX cmp_inventory_schedule_account_items;

CREATE INDEX cmp_inventory_schedule_account_items ON cmp_inventory_schedules (tenant_id,account_id,schedule_id)
    WHERE target_id='' OR kind IN ('database-backups','database-restore-windows');
