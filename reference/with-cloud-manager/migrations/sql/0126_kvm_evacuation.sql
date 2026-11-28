ALTER TABLE cmp_kvm_placements ADD COLUMN evacuation_vm_id uuid REFERENCES cmp_kvm_vms;

ALTER TABLE cmp_kvm_cold_migrations
    ADD COLUMN source_fence_generation bigint NOT NULL DEFAULT 0 CHECK (source_fence_generation>=0),
    ADD COLUMN cleanup_sha256 text NOT NULL DEFAULT '',
    ADD COLUMN cleanup_reviewer_id text,
    ADD COLUMN cleanup_at timestamptz,
    ADD FOREIGN KEY (tenant_id,project_id,cleanup_reviewer_id) REFERENCES cmp_memberships,
    ADD CHECK ((cleanup_at IS NULL AND cleanup_reviewer_id IS NULL AND cleanup_sha256='')
        OR (cleanup_at IS NOT NULL AND source_fence_generation>0 AND status='approved'
            AND cleanup_reviewer_id IS NOT NULL AND cleanup_reviewer_id<>requester_id
            AND cleanup_sha256 ~ '^[0-9a-f]{64}$'));

CREATE UNIQUE INDEX cmp_kvm_evacuation_cleanup ON cmp_kvm_cold_migrations
    (tenant_id,project_id,site_id,source_host,vm_id)
    WHERE source_fence_generation>0 AND status='approved' AND cleanup_at IS NULL;
