SET LOCAL lock_timeout = '2s';

CREATE TABLE cmp_instance_resize_options (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    project_id text NOT NULL,
    principal_id text NOT NULL,
    credential_revision bigint NOT NULL,
    ownership_revision bigint NOT NULL,
    current_type text NOT NULL,
    fence bigint NOT NULL DEFAULT 0,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','ready','unavailable')),
    due_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    expires_at timestamptz NOT NULL,
    observed_at timestamptz,
    zone text NOT NULL DEFAULT '',
    target_types jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(target_types)='array'),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects(scope_id,external_id) ON DELETE CASCADE
);

CREATE INDEX cmp_instance_resize_options_due ON cmp_instance_resize_options(due_at,scope_id,external_id)
    WHERE state='pending';
