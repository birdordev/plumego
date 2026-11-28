ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operations_action_check;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operations_action_check
    CHECK (action IN ('start','stop','reboot','delete','create'));
ALTER TABLE cmp_instance_operations ALTER COLUMN scope_id DROP NOT NULL;
ALTER TABLE cmp_instance_operations ALTER COLUMN external_id DROP NOT NULL;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operation_target CHECK (
    (scope_id IS NOT NULL AND external_id IS NOT NULL) OR
    (action='create' AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
);
ALTER TABLE cmp_instance_approval_rules DROP CONSTRAINT cmp_instance_approval_rules_action_check;
ALTER TABLE cmp_instance_approval_rules ADD CONSTRAINT cmp_instance_approval_rules_action_check
    CHECK (action IN ('start','stop','reboot','create'));

ALTER TABLE cmp_instance_operation_budgets ADD COLUMN max_managed_instances integer NOT NULL DEFAULT 100
    CHECK (max_managed_instances BETWEEN 0 AND 100000);

CREATE TABLE cmp_instance_offerings (
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    size text NOT NULL,
    PRIMARY KEY (tenant_id,account_id,region,size),
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts
);

CREATE TABLE cmp_instance_launches (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    launch_token text NOT NULL UNIQUE,
    name text NOT NULL,
    image_id text NOT NULL,
    size text NOT NULL,
    network_id text NOT NULL,
    security_group_id text NOT NULL,
    image_revision bigint NOT NULL,
    network_revision bigint NOT NULL,
    security_group_revision bigint NOT NULL,
    dispatch_started_at timestamptz,
    replayed_at timestamptz,
    released_at timestamptz
);

CREATE TABLE cmp_instance_observation_retries (
    operation_id text NOT NULL REFERENCES cmp_instance_operations,
    tenant_id text NOT NULL,
    reviewer_id text NOT NULL,
    evidence_ref text NOT NULL CHECK (octet_length(evidence_ref) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (operation_id,reviewer_id,evidence_ref),
    FOREIGN KEY (tenant_id,reviewer_id) REFERENCES cmp_principals
);
