ALTER TABLE cmp_inventory_schedules
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule','elastic-ip','snapshot','load-balancer','dns-zone','database-instance','object-bucket')),
    ADD CONSTRAINT cmp_inventory_schedules_object_bucket_scope CHECK (kind<>'object-bucket' OR region='global');
