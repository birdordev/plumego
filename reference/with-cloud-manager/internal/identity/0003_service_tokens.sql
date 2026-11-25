CREATE TABLE cmp_service_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    FOREIGN KEY (tenant_id, principal_id) REFERENCES cmp_principals
);
