CREATE TABLE cmp_tag_policies (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    max_age_seconds integer NOT NULL CHECK (max_age_seconds BETWEEN 60 AND 604800),
    rules jsonb NOT NULL CHECK (jsonb_typeof(rules)='array'),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_by text NOT NULL,
    PRIMARY KEY (tenant_id,project_id,revision),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,updated_by) REFERENCES cmp_principals
);

ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation',
                        'catalog','alert','console_access','billing_budget','tag_policy')) NOT VALID;
