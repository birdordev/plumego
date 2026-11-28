ALTER TABLE cmp_inventory_schedules
    ADD COLUMN config_revision bigint NOT NULL DEFAULT 1 CHECK (config_revision>0);

CREATE INDEX cmp_inventory_schedule_account_items ON cmp_inventory_schedules (tenant_id,account_id,schedule_id)
    WHERE target_id='';
