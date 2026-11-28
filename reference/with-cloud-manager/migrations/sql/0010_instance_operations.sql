CREATE TABLE cmp_instance_operations (
    operation_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    principal_id text NOT NULL,
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    action text NOT NULL CHECK (action IN ('start','stop','reboot')),
    status text NOT NULL CHECK (status IN ('queued','applying','observing','succeeded','failed','needs_review')),
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    credential_revision bigint NOT NULL,
    membership_revision bigint NOT NULL,
    ownership_revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (tenant_id,project_id,principal_id,idempotency_key),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,principal_id) REFERENCES cmp_principals,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects
);

CREATE UNIQUE INDEX cmp_instance_active ON cmp_instance_operations (scope_id,external_id)
    WHERE status IN ('queued','applying','observing','needs_review');
CREATE INDEX cmp_instance_active_project ON cmp_instance_operations (tenant_id,project_id)
    WHERE status IN ('queued','applying','observing','needs_review');

CREATE TABLE cmp_instance_operation_budgets (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    max_active integer NOT NULL DEFAULT 100 CHECK (max_active BETWEEN 1 AND 10000),
    PRIMARY KEY (tenant_id,project_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);

CREATE TABLE cmp_instance_operation_events (
    event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation_id text NOT NULL REFERENCES cmp_instance_operations,
    status text NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
