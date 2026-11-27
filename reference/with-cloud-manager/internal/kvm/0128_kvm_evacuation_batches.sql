CREATE TABLE cmp_kvm_evacuation_batches (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    source_host text NOT NULL,
    batch_id text NOT NULL,
    source_fence_generation bigint NOT NULL CHECK (source_fence_generation>0),
    host_revision bigint NOT NULL CHECK (host_revision>0),
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    request_sha256 bytea NOT NULL CHECK (octet_length(request_sha256)=32),
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,site_id,source_host,batch_id),
    FOREIGN KEY (tenant_id,project_id,site_id,source_host) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships
);

CREATE TABLE cmp_kvm_evacuation_batch_items (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    source_host text NOT NULL,
    batch_id text NOT NULL,
    position integer NOT NULL CHECK (position BETWEEN 1 AND 16),
    vm_id uuid NOT NULL,
    migration_id text NOT NULL,
    PRIMARY KEY (tenant_id,project_id,site_id,source_host,batch_id,position),
    FOREIGN KEY (tenant_id,project_id,site_id,source_host,batch_id) REFERENCES cmp_kvm_evacuation_batches,
    FOREIGN KEY (vm_id,migration_id) REFERENCES cmp_kvm_cold_migrations,
    UNIQUE (vm_id,migration_id)
);
