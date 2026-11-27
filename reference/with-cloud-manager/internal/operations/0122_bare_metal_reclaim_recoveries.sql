ALTER TABLE cmp_bare_metal_reclaims ADD UNIQUE (tenant_id,project_id,reclaim_id);

CREATE TABLE cmp_bare_metal_reclaim_recoveries (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    reclaim_id text NOT NULL,
    recovery_id text NOT NULL,
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    credential_revision bigint NOT NULL CHECK (credential_revision>0),
    isolation_sha256 text NOT NULL CHECK (isolation_sha256 ~ '^[0-9a-f]{64}$'),
    evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    reviewer_id text,
    state text NOT NULL DEFAULT 'requested' CHECK (state IN ('requested','verified','rejected')),
    requested_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    reviewed_at timestamptz,
    PRIMARY KEY (reclaim_id,recovery_id),
    FOREIGN KEY (tenant_id,project_id,reclaim_id) REFERENCES cmp_bare_metal_reclaims(tenant_id,project_id,reclaim_id),
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,reviewer_id) REFERENCES cmp_memberships,
    CHECK (reviewer_id<>requester_id),
    CHECK ((state='requested')=(reviewer_id IS NULL) AND (reviewer_id IS NULL)=(reviewed_at IS NULL))
);

CREATE UNIQUE INDEX cmp_bare_metal_recovery_open ON cmp_bare_metal_reclaim_recoveries (reclaim_id)
    WHERE state IN ('requested','verified');
CREATE INDEX cmp_bare_metal_recovery_recent ON cmp_bare_metal_reclaim_recoveries (reclaim_id,requested_at DESC,recovery_id);
