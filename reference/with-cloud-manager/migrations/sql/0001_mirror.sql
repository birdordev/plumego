CREATE TABLE cmp_cmdb_mirrors (
    source_url text NOT NULL,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    issued_generation bigint NOT NULL CHECK (issued_generation > 0),
    published_generation bigint NOT NULL DEFAULT 0 CHECK (published_generation >= 0 AND published_generation <= issued_generation),
    captured_at timestamptz,
    PRIMARY KEY (source_url,tenant_id,project_id)
);

CREATE TABLE cmp_cmdb_assets (
    source_url text NOT NULL,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    kind text NOT NULL,
    asset_id text NOT NULL,
    asset jsonb NOT NULL,
    PRIMARY KEY (source_url,tenant_id,project_id,account_id,region,kind,asset_id),
    FOREIGN KEY (source_url,tenant_id,project_id) REFERENCES cmp_cmdb_mirrors
);

CREATE TABLE cmp_cmdb_reconciliations (
    source_url text NOT NULL,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    captured_at timestamptz NOT NULL,
    published_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    asset_count integer NOT NULL CHECK (asset_count >= 0),
    upserted bigint NOT NULL CHECK (upserted >= 0),
    removed bigint NOT NULL CHECK (removed >= 0),
    digest text NOT NULL CHECK (digest ~ '^[a-f0-9]{64}$'),
    PRIMARY KEY (source_url,tenant_id,project_id,generation),
    FOREIGN KEY (source_url,tenant_id,project_id) REFERENCES cmp_cmdb_mirrors
);
