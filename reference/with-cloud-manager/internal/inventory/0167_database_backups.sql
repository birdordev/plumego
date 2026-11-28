CREATE TABLE cmp_database_backups (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    product text NOT NULL,
    engine text NOT NULL,
    credential_revision bigint NOT NULL CHECK (credential_revision > 0),
    refresh_started_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    backups jsonb NOT NULL CHECK (jsonb_typeof(backups)='array' AND jsonb_array_length(backups)<=5000),
    CHECK (window_start < window_end),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
