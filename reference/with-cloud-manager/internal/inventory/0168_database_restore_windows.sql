CREATE TABLE cmp_database_restore_windows (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    product text NOT NULL,
    engine text NOT NULL,
    credential_revision bigint NOT NULL CHECK (credential_revision > 0),
    refresh_started_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    recovery_scope text NOT NULL CHECK (recovery_scope IN ('instance','source_instance')),
    windows jsonb NOT NULL CHECK (jsonb_typeof(windows)='array' AND jsonb_array_length(windows)<=512),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
