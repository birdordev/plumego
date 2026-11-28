CREATE TABLE cmp_cluster_workloads (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    credential_revision bigint NOT NULL,
    refresh_started_at timestamptz NOT NULL,
    observed_at timestamptz NOT NULL,
    snapshot_id uuid NOT NULL DEFAULT gen_random_uuid(),
    workloads jsonb NOT NULL CHECK (jsonb_typeof(workloads)='array' AND jsonb_array_length(workloads)<=5000),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE
);
