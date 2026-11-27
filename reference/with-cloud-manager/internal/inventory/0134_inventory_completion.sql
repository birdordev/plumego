ALTER TABLE cmp_inventory_scopes ADD COLUMN completed_credential_revision bigint;

ALTER TABLE cmp_inventory_scopes ADD CONSTRAINT cmp_inventory_completed_credential_revision
CHECK (completed_credential_revision IS NULL OR
    (completed_at IS NOT NULL AND completed_credential_revision BETWEEN 1 AND credential_revision));
