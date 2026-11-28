CREATE TABLE cmp_operation_batches (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    principal_id text NOT NULL,
    idempotency_key text NOT NULL,
    batch_id text NOT NULL UNIQUE,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    item_count integer NOT NULL CHECK (item_count BETWEEN 1 AND 25),
    PRIMARY KEY (tenant_id,project_id,principal_id,idempotency_key),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,principal_id) REFERENCES cmp_principals
);

CREATE TABLE cmp_operation_batch_items (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    principal_id text NOT NULL,
    batch_key text NOT NULL,
    position integer NOT NULL CHECK (position BETWEEN 0 AND 24),
    operation_key text NOT NULL,
    PRIMARY KEY (tenant_id,project_id,principal_id,batch_key,position),
    UNIQUE (tenant_id,project_id,principal_id,operation_key),
    FOREIGN KEY (tenant_id,project_id,principal_id,batch_key) REFERENCES cmp_operation_batches,
    FOREIGN KEY (tenant_id,project_id,principal_id,operation_key)
        REFERENCES cmp_instance_operations(tenant_id,project_id,principal_id,idempotency_key)
);
