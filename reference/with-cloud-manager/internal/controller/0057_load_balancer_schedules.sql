ALTER TABLE cmp_inventory_schedules
    DROP CONSTRAINT cmp_inventory_schedules_kind_check,
    ADD CONSTRAINT cmp_inventory_schedules_kind_check CHECK (
        kind IN ('region','zone','instance','volume','network','subnet','security-group','image','security-group-rule','elastic-ip','snapshot','load-balancer'));
