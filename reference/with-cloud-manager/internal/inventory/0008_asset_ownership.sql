ALTER TABLE cmp_inventory_scopes ADD CONSTRAINT cmp_inventory_scope_tenant UNIQUE (tenant_id,scope_id);

CREATE TABLE cmp_asset_ownership (
    tenant_id text NOT NULL,
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    project_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision >= 2),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects,
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);

CREATE TABLE cmp_asset_ownership_events (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    tenant_id text NOT NULL,
    actor_id text NOT NULL,
    from_project text NOT NULL,
    to_project text NOT NULL,
    revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (scope_id,external_id,revision),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects,
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES cmp_principals,
    FOREIGN KEY (tenant_id,from_project) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,to_project) REFERENCES cmp_projects
);
