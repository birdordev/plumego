CREATE TABLE cmp_kvm_shared_storage (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    pool_id text NOT NULL,
    server_address text NOT NULL,
    export_path text NOT NULL,
    capacity_mib bigint NOT NULL CHECK (capacity_mib BETWEEN 1 AND 1099511627776),
    registered_by text NOT NULL,
    registered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,site_id,pool_id),
    UNIQUE (server_address,export_path),
    FOREIGN KEY (tenant_id,project_id,registered_by) REFERENCES cmp_memberships
);

ALTER TABLE cmp_kvm_hosts ADD COLUMN shared_storage_pool_id text,
    ADD FOREIGN KEY (tenant_id,project_id,site_id,shared_storage_pool_id) REFERENCES cmp_kvm_shared_storage;

ALTER TABLE cmp_kvm_placements ADD COLUMN shared_storage_pool_id text,
    ADD FOREIGN KEY (tenant_id,project_id,site_id,shared_storage_pool_id) REFERENCES cmp_kvm_shared_storage,
    ADD CHECK (shared_storage_pool_id IS NULL OR boot_volume='' OR image_id IS NOT NULL);

CREATE INDEX cmp_kvm_shared_storage_holds ON cmp_kvm_placements
    (tenant_id,project_id,site_id,shared_storage_pool_id,expires_at)
    WHERE shared_storage_pool_id IS NOT NULL AND ((released_at IS NULL AND expired_at IS NULL) OR boot_volume_retained);
CREATE UNIQUE INDEX cmp_kvm_shared_storage_volume ON cmp_kvm_placements
    (tenant_id,project_id,site_id,shared_storage_pool_id,boot_volume)
    WHERE shared_storage_pool_id IS NOT NULL AND boot_volume<>'' AND (released_at IS NULL OR boot_volume_retained);
