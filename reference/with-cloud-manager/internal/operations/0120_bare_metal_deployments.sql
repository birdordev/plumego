CREATE TABLE cmp_bare_metal_deployments (
    deployment_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    account_id text NOT NULL,
    site_id text NOT NULL,
    system_id text NOT NULL,
    stage_operation text NOT NULL UNIQUE REFERENCES cmp_instance_operations,
    boot_operation text NOT NULL UNIQUE REFERENCES cmp_instance_operations,
    credential_revision bigint NOT NULL CHECK (credential_revision>0),
    ownership_revision bigint NOT NULL CHECK (ownership_revision>0),
    image_sha256 text NOT NULL CHECK (image_sha256 ~ '^[0-9a-f]{64}$'),
    installer_id text NOT NULL,
    installer_membership bigint NOT NULL CHECK (installer_membership>0),
    installer_generation bigint NOT NULL CHECK (installer_generation>0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    expires_at timestamptz NOT NULL DEFAULT (statement_timestamp()+interval '2 hours'),
    result text CHECK (result IN ('installed','failed')),
    evidence_sha256 text CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    recorded_at timestamptz,
    FOREIGN KEY (tenant_id,project_id,installer_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    CHECK (expires_at>created_at AND stage_operation<>boot_operation),
    CHECK ((result IS NULL)=(recorded_at IS NULL) AND (result IS NULL)=(evidence_sha256 IS NULL))
);

CREATE INDEX cmp_bare_metal_deployment_active ON cmp_bare_metal_deployments(system_id,expires_at)
    WHERE result IS NULL;
