CREATE TABLE cmp_bare_metal_reclaims (
    reclaim_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    scope_id bigint NOT NULL,
    account_id text NOT NULL,
    site_id text NOT NULL,
    system_id text NOT NULL,
    ownership_revision bigint NOT NULL CHECK (ownership_revision>0),
    credential_revision bigint NOT NULL CHECK (credential_revision>0),
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    request_key text NOT NULL,
    sanitizer_id text NOT NULL,
    sanitizer_revision bigint NOT NULL CHECK (sanitizer_revision>0),
    sanitizer_generation bigint NOT NULL CHECK (sanitizer_generation>0),
    reviewer_id text,
    reviewer_revision bigint CHECK (reviewer_revision>0),
    state text NOT NULL CHECK (state IN ('requested','approved','rejected','claimed','verified','consumed')),
    requested_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    reviewed_at timestamptz,
    claimed_at timestamptz,
    verified_at timestamptz,
    consumed_at timestamptz,
    evidence_sha256 text CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    verified_boot_event bigint CHECK (verified_boot_event>=0),
    UNIQUE (tenant_id,project_id,requester_id,request_key),
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,sanitizer_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,reviewer_id) REFERENCES cmp_memberships,
    CHECK (requester_id<>sanitizer_id AND reviewer_id NOT IN (requester_id,sanitizer_id)),
    CHECK ((state='requested')=(reviewer_id IS NULL) AND (reviewer_id IS NULL)=(reviewed_at IS NULL)
           AND (reviewer_id IS NULL)=(reviewer_revision IS NULL)),
    CHECK ((state IN ('claimed','verified','consumed'))=(claimed_at IS NOT NULL)),
    CHECK ((state IN ('verified','consumed'))=(verified_at IS NOT NULL)
           AND (verified_at IS NULL)=(evidence_sha256 IS NULL) AND (verified_at IS NULL)=(verified_boot_event IS NULL)),
    CHECK ((state='consumed')=(consumed_at IS NOT NULL))
);

CREATE UNIQUE INDEX cmp_bare_metal_reclaim_open ON cmp_bare_metal_reclaims (system_id)
    WHERE state IN ('requested','approved','claimed','verified');
CREATE INDEX cmp_bare_metal_boot_history ON cmp_instance_operations (external_id,operation_id)
    WHERE resource_kind='bare-metal' AND action='power-on-bare-metal';
