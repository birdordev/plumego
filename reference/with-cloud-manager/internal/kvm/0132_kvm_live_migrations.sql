CREATE TABLE cmp_kvm_live_migrations (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    vm_id uuid NOT NULL REFERENCES cmp_kvm_vms,
    migration_id text NOT NULL,
    placement_id text NOT NULL REFERENCES cmp_kvm_placements,
    source_host text NOT NULL,
    target_host text NOT NULL,
    source_generation bigint NOT NULL CHECK (source_generation>0),
    target_generation bigint NOT NULL CHECK (target_generation>0),
    vm_revision bigint NOT NULL CHECK (vm_revision>0),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','approved','dispatched','source_departed','completed','rejected','aborted')),
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    reviewer_id text,
    reviewer_revision bigint,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reviewed_at timestamptz,
    prepared_at timestamptz,
    network_sha256 text NOT NULL DEFAULT '',
    source_mac text NOT NULL DEFAULT '',
    dispatched_at timestamptz,
    source_departed_at timestamptz,
    completed_at timestamptz,
    aborted_at timestamptz,
    aborted_by text,
    abort_evidence text NOT NULL DEFAULT '',
    PRIMARY KEY (vm_id,migration_id),
    FOREIGN KEY (tenant_id,project_id,site_id,source_host) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,site_id,target_host) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,reviewer_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,aborted_by) REFERENCES cmp_memberships,
    CHECK (source_host<>target_host),
    CHECK ((status='pending')=(reviewer_id IS NULL) AND ((reviewer_id IS NULL)=(reviewed_at IS NULL))
        AND ((reviewer_id IS NULL)=(reviewer_revision IS NULL)) AND (reviewer_id IS NULL OR reviewer_id<>requester_id)),
    CHECK ((prepared_at IS NULL)=(network_sha256='') AND (network_sha256='' OR network_sha256 ~ '^[a-f0-9]{64}$')),
    CHECK ((status IN ('dispatched','source_departed','completed','aborted'))=(dispatched_at IS NOT NULL)),
    CHECK ((dispatched_at IS NULL)=(source_mac='') AND (source_mac='' OR source_mac ~ '^([a-f0-9]{2}:){5}[a-f0-9]{2}$')),
    CHECK (status='aborted' OR (status IN ('source_departed','completed'))=(source_departed_at IS NOT NULL)),
    CHECK ((status='completed')=(completed_at IS NOT NULL)),
    CHECK ((status='aborted')=(aborted_at IS NOT NULL) AND ((aborted_at IS NULL)=(aborted_by IS NULL))
        AND ((aborted_at IS NULL)=(abort_evidence='')) AND (abort_evidence='' OR abort_evidence ~ '^[a-f0-9]{64}$'))
);

CREATE UNIQUE INDEX cmp_kvm_live_migration_active ON cmp_kvm_live_migrations(vm_id)
    WHERE status IN ('pending','approved','dispatched','source_departed');
CREATE INDEX cmp_kvm_live_migration_source ON cmp_kvm_live_migrations(tenant_id,project_id,site_id,source_host)
    WHERE status IN ('pending','approved','dispatched','source_departed');
CREATE INDEX cmp_kvm_live_migration_target ON cmp_kvm_live_migrations(tenant_id,project_id,site_id,target_host)
    WHERE status IN ('pending','approved','dispatched','source_departed');
