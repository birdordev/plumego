CREATE TABLE cmp_instance_catalog_items (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    template_id text NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    revision bigint NOT NULL CHECK (revision >= version),
    enabled boolean NOT NULL,
    PRIMARY KEY (tenant_id,project_id,template_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);

CREATE TABLE cmp_instance_catalog_versions (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    template_id text NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    title text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    image_id text NOT NULL,
    size text NOT NULL,
    network_id text NOT NULL,
    security_group_id text NOT NULL,
    published_by text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,template_id,version),
    FOREIGN KEY (tenant_id,project_id,template_id) REFERENCES cmp_instance_catalog_items,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id,published_by) REFERENCES cmp_principals
);

ALTER TABLE cmp_instance_catalog_items ADD CONSTRAINT cmp_instance_catalog_current
    FOREIGN KEY (tenant_id,project_id,template_id,version) REFERENCES cmp_instance_catalog_versions
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE cmp_instance_catalog_events (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    template_id text NOT NULL,
    version bigint NOT NULL,
    revision bigint NOT NULL CHECK (revision >= version),
    actor_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('publish','retire')),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,template_id,revision),
    FOREIGN KEY (tenant_id,project_id,template_id,version) REFERENCES cmp_instance_catalog_versions,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES cmp_principals
);

ALTER TABLE cmp_instance_operations
    ADD COLUMN catalog_id text,
    ADD COLUMN catalog_version bigint,
    ADD CONSTRAINT cmp_instance_operation_catalog_pair CHECK (
        (catalog_id IS NULL AND catalog_version IS NULL) OR
        (action='create' AND catalog_id IS NOT NULL AND catalog_version IS NOT NULL)),
    ADD CONSTRAINT cmp_instance_operation_catalog_version
        FOREIGN KEY (tenant_id,project_id,catalog_id,catalog_version) REFERENCES cmp_instance_catalog_versions;
