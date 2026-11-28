CREATE TABLE cmp_kvm_host_images (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    host_id text NOT NULL,
    image_id text NOT NULL,
    host_generation bigint NOT NULL CHECK (host_generation>0),
    agent_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    state text NOT NULL CHECK (state IN ('verified','missing')),
    reported_at timestamptz NOT NULL,
    expires_at timestamptz,
    PRIMARY KEY (tenant_id,project_id,site_id,host_id,image_id),
    FOREIGN KEY (tenant_id,project_id,site_id,host_id) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,site_id,image_id) REFERENCES cmp_kvm_boot_images,
    FOREIGN KEY (tenant_id,project_id,agent_id) REFERENCES cmp_memberships,
    CHECK ((state='verified')=(expires_at IS NOT NULL)),
    CHECK (expires_at IS NULL OR expires_at>reported_at)
);

ALTER TABLE cmp_kvm_cold_migrations ADD COLUMN require_verified_backup boolean NOT NULL DEFAULT false;
