ALTER TABLE cmp_kvm_hosts ADD COLUMN fence_state text NOT NULL DEFAULT ''
    CHECK (fence_state IN ('','pending','fenced','rejoin_pending'));
ALTER TABLE cmp_kvm_hosts ADD CHECK
    (fence_state='' OR (draining AND reported_at IS NULL AND sequence=0
        AND vcpu=0 AND memory_mib=0 AND disk_mib=0 AND gpu=0));

CREATE TABLE cmp_kvm_host_fence_events (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    host_id text NOT NULL,
    host_revision bigint NOT NULL CHECK (host_revision>0),
    host_generation bigint NOT NULL CHECK (host_generation>0),
    actor_id text NOT NULL,
    actor_revision bigint NOT NULL CHECK (actor_revision>0),
    action text NOT NULL CHECK (action IN
        ('request','confirm','request-rejoin','approve-rejoin','reject-rejoin')),
    evidence_sha256 text NOT NULL,
    replacement_agent text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,site_id,host_id,host_revision),
    FOREIGN KEY (tenant_id,project_id,site_id,host_id) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,actor_id) REFERENCES cmp_memberships,
    CHECK (CASE WHEN action IN ('confirm','request-rejoin')
           THEN evidence_sha256 ~ '^[0-9a-f]{64}$' ELSE evidence_sha256='' END),
    CHECK ((action='request-rejoin')=(replacement_agent<>''))
);
