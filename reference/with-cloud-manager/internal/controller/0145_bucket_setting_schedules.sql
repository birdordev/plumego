ALTER TABLE cmp_inventory_schedules
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule',
                 'elastic-ip','snapshot','load-balancer','dns-zone','database-instance','object-bucket','cache-instance',
                 'kubernetes-cluster','kubernetes-node-pools','kubernetes-workloads','kubernetes-workload-watch','instance-idle',
                 'bare-metal','object-bucket-encryption','object-bucket-versioning')),
    ADD CONSTRAINT cmp_inventory_schedules_bucket_setting_scope CHECK (
        kind NOT IN ('object-bucket-encryption','object-bucket-versioning') OR (region='global' AND target_id='')),
    ADD COLUMN bucket_cursor text NOT NULL DEFAULT '' CHECK (length(bucket_cursor)<=128),
    ADD COLUMN bucket_cursor_revision bigint NOT NULL DEFAULT 0 CHECK (bucket_cursor_revision>=0),
    ADD COLUMN bucket_batch_rejected integer CHECK (bucket_batch_rejected BETWEEN 0 AND 16);
