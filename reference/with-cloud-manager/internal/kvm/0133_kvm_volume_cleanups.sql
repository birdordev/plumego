CREATE TABLE cmp_kvm_volume_cleanups (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    placement_id text NOT NULL REFERENCES cmp_kvm_placements,
    cleanup_id text NOT NULL,
    vm_id uuid NOT NULL REFERENCES cmp_kvm_vms,
    host_id text NOT NULL,
    host_generation bigint NOT NULL CHECK (host_generation>0),
    host_revision bigint NOT NULL CHECK (host_revision>0),
    backup_image_id text NOT NULL,
    evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[a-f0-9]{64}$'),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','dispatched','completed','rejected','aborted')),
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    reviewer_id text,
    reviewer_revision bigint,
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reviewed_at timestamptz,
    dispatched_at timestamptz,
    pool_sha256 text NOT NULL DEFAULT '',
    first_absent_at timestamptz,
    finished_at timestamptz,
    resolved_by text,
    resolution_sha256 text NOT NULL DEFAULT '',
    second_resolution_sha256 text NOT NULL DEFAULT '',
    PRIMARY KEY (placement_id,cleanup_id),
    FOREIGN KEY (tenant_id,project_id,site_id,host_id) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,site_id,backup_image_id) REFERENCES cmp_kvm_boot_images,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,reviewer_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,resolved_by) REFERENCES cmp_memberships,
    CHECK ((status='pending')=(reviewer_id IS NULL) AND ((reviewer_id IS NULL)=(reviewed_at IS NULL))
        AND ((reviewer_id IS NULL)=(reviewer_revision IS NULL)) AND (reviewer_id IS NULL OR reviewer_id<>requester_id)),
    CHECK ((status IN ('dispatched','completed','aborted'))=(dispatched_at IS NOT NULL)),
    CHECK ((dispatched_at IS NULL)=(pool_sha256='') AND (pool_sha256='' OR pool_sha256 ~ '^[a-f0-9]{64}$')),
    CHECK (first_absent_at IS NULL OR dispatched_at IS NOT NULL),
    CHECK ((status IN ('completed','rejected','aborted'))=(finished_at IS NOT NULL)),
    CHECK ((resolved_by IS NULL)=(resolution_sha256='') AND (resolution_sha256='' OR resolution_sha256 ~ '^[a-f0-9]{64}$')),
    CHECK (second_resolution_sha256='' OR (second_resolution_sha256 ~ '^[a-f0-9]{64}$' AND status='completed'
        AND resolved_by IS NOT NULL AND second_resolution_sha256<>resolution_sha256)),
    CHECK (status<>'aborted' OR resolved_by IS NOT NULL),
    CHECK (status<>'completed' OR first_absent_at IS NOT NULL OR second_resolution_sha256<>'')
);

CREATE UNIQUE INDEX cmp_kvm_volume_cleanup_active ON cmp_kvm_volume_cleanups(placement_id)
    WHERE status IN ('pending','approved','dispatched');
CREATE INDEX cmp_kvm_volume_cleanup_host ON cmp_kvm_volume_cleanups(tenant_id,project_id,site_id,host_id,placement_id)
    WHERE status IN ('pending','approved','dispatched');
