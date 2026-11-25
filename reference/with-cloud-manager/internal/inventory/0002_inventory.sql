CREATE TABLE cmp_inventory_scopes (
    scope_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    kind text NOT NULL,
    credential_revision bigint NOT NULL,
    generation bigint NOT NULL DEFAULT 1,
    fence bigint NOT NULL DEFAULT 1,
    cursor text NOT NULL DEFAULT '',
    pages integer NOT NULL DEFAULT 0 CHECK (pages BETWEEN 0 AND 10000),
    running boolean NOT NULL DEFAULT true,
    lease_until timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (tenant_id, account_id, region, kind),
    FOREIGN KEY (tenant_id, account_id) REFERENCES cmp_cloud_accounts
);

CREATE TABLE cmp_inventory_cursors (
    scope_id bigint NOT NULL REFERENCES cmp_inventory_scopes ON DELETE CASCADE,
    fingerprint bytea NOT NULL,
    PRIMARY KEY (scope_id, fingerprint)
);

CREATE TABLE cmp_inventory_objects (
    scope_id bigint NOT NULL REFERENCES cmp_inventory_scopes ON DELETE CASCADE,
    external_id text NOT NULL,
    name text NOT NULL,
    status text NOT NULL,
    seen_generation bigint NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    missing_generation bigint,
    removed_at timestamptz,
    PRIMARY KEY (scope_id, external_id)
);

CREATE INDEX cmp_inventory_missing ON cmp_inventory_objects (scope_id, missing_generation)
    WHERE missing_generation IS NOT NULL AND removed_at IS NULL;
