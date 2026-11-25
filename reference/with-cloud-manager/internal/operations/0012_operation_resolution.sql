CREATE TABLE cmp_instance_operation_resolutions (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    tenant_id text NOT NULL,
    reviewer_id text NOT NULL,
    decision text NOT NULL CHECK (decision IN ('succeeded','failed')),
    evidence_ref text NOT NULL CHECK (octet_length(evidence_ref) BETWEEN 1 AND 200),
    request_id text NOT NULL CHECK (octet_length(request_id) <= 128),
    reviewed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (tenant_id,reviewer_id) REFERENCES cmp_principals
);
