CREATE TABLE cmp_load_balancer_configurations (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    credential_revision bigint NOT NULL CHECK (credential_revision > 0),
    refresh_started_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    configuration jsonb NOT NULL CHECK (
        jsonb_typeof(configuration)='object'
        AND COALESCE(jsonb_typeof(configuration->'listeners')='array',false)
        AND COALESCE(jsonb_typeof(configuration->'target_groups')='array',false)),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
