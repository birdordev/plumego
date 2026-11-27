CREATE TABLE cmp_kvm_cold_migrations (
    vm_id uuid NOT NULL REFERENCES cmp_kvm_vms,
    migration_id text NOT NULL,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    source_host text NOT NULL,
    target_host text NOT NULL,
    source_placement_id text NOT NULL REFERENCES cmp_kvm_placements,
    target_placement_id text NOT NULL REFERENCES cmp_kvm_placements,
    target_generation bigint NOT NULL CHECK (target_generation>0),
    vm_revision bigint NOT NULL CHECK (vm_revision>0),
    image_id text NOT NULL,
    evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    reviewer_id text,
    reviewer_revision bigint CHECK (reviewer_revision>0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reviewed_at timestamptz,
    PRIMARY KEY (vm_id,migration_id),
    FOREIGN KEY (tenant_id,project_id,site_id,source_host) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,site_id,target_host) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,site_id,image_id) REFERENCES cmp_kvm_boot_images,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,reviewer_id) REFERENCES cmp_memberships,
    CHECK (source_host<>target_host AND source_placement_id<>target_placement_id),
    CHECK (reviewer_id<>requester_id),
    CHECK ((status='pending')=(reviewed_at IS NULL)
           AND (reviewed_at IS NULL)=(reviewer_id IS NULL)
           AND (reviewer_id IS NULL)=(reviewer_revision IS NULL))
);

CREATE UNIQUE INDEX cmp_kvm_cold_migration_pending ON cmp_kvm_cold_migrations(vm_id) WHERE status='pending';
CREATE UNIQUE INDEX cmp_kvm_cold_migration_lineage ON cmp_kvm_cold_migrations(source_placement_id) WHERE status='approved';
CREATE INDEX cmp_kvm_cold_migration_history ON cmp_kvm_cold_migrations(vm_id,requested_at DESC,migration_id DESC);
