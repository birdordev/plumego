CREATE TABLE cmp_cloud_account_events (
    event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    actor_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('register','rotate','suspend','resume','transfer','share','unshare')),
    project_id text NOT NULL,
    target_project text,
    revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES cmp_principals,
    UNIQUE (tenant_id,account_id,revision)
);
