CREATE TABLE cmp_asset_tag_rules (
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    tag_key text NOT NULL CHECK (octet_length(tag_key) BETWEEN 1 AND 128),
    tag_value text NOT NULL CHECK (octet_length(tag_value) <= 256),
    project_id text NOT NULL,
    PRIMARY KEY (tenant_id,account_id,tag_key,tag_value),
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);

CREATE TABLE cmp_asset_tag_assignments (
    tenant_id text NOT NULL,
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('inherited','mapped','conflict')),
    project_id text,
    revision bigint NOT NULL CHECK (revision >= 2),
    CHECK ((state='mapped')=(project_id IS NOT NULL)),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects,
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);
