ALTER TABLE cmp_inventory_objects ADD COLUMN deletion_confirmed_at timestamptz;
ALTER TABLE cmp_inventory_objects ADD CONSTRAINT cmp_inventory_confirmed_deletion_removed
    CHECK (deletion_confirmed_at IS NULL OR removed_at IS NOT NULL);
