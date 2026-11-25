CREATE TABLE cmp_browser_sessions (
    session_hash bytea PRIMARY KEY CHECK (octet_length(session_hash)=32),
    tenant_id text NOT NULL,
    principal_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    FOREIGN KEY (tenant_id,principal_id) REFERENCES cmp_principals
);

CREATE INDEX cmp_browser_session_expiry ON cmp_browser_sessions (expires_at);
