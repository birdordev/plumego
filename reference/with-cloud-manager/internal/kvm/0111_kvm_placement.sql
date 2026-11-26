CREATE TABLE cmp_kvm_hosts (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    host_id text NOT NULL,
    host_uuid uuid NOT NULL UNIQUE,
    agent_id text NOT NULL,
    failure_domain text NOT NULL,
    networks jsonb NOT NULL CHECK (jsonb_typeof(networks)='array' AND jsonb_array_length(networks) BETWEEN 1 AND 32),
    storage_pool text NOT NULL,
    gpu_model text NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
    draining boolean NOT NULL DEFAULT false,
    sequence bigint NOT NULL DEFAULT 0 CHECK (sequence>=0),
    reported_at timestamptz,
    vcpu integer NOT NULL DEFAULT 0 CHECK (vcpu BETWEEN 0 AND 4096),
    memory_mib bigint NOT NULL DEFAULT 0 CHECK (memory_mib BETWEEN 0 AND 16777216),
    disk_mib bigint NOT NULL DEFAULT 0 CHECK (disk_mib BETWEEN 0 AND 1099511627776),
    gpu integer NOT NULL DEFAULT 0 CHECK (gpu BETWEEN 0 AND 128 AND (gpu=0 OR gpu_model<>'')),
    PRIMARY KEY (tenant_id,project_id,site_id,host_id),
    UNIQUE (tenant_id,agent_id),
    FOREIGN KEY (tenant_id,project_id,agent_id) REFERENCES cmp_memberships,
    CHECK (reported_at IS NULL OR sequence>0)
);

CREATE TABLE cmp_kvm_placements (
    placement_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    host_id text NOT NULL,
    generation bigint NOT NULL CHECK (generation>0),
    requester_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    network_id text NOT NULL,
    storage_pool text NOT NULL,
    gpu_model text NOT NULL,
    vcpu integer NOT NULL CHECK (vcpu BETWEEN 1 AND 4096),
    memory_mib bigint NOT NULL CHECK (memory_mib BETWEEN 1 AND 16777216),
    disk_mib bigint NOT NULL CHECK (disk_mib BETWEEN 1 AND 1099511627776),
    gpu integer NOT NULL CHECK (gpu BETWEEN 0 AND 128 AND ((gpu=0)=(gpu_model=''))),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at>created_at),
    released_at timestamptz,
    expired_at timestamptz,
    CHECK (released_at IS NULL OR expired_at IS NULL),
    UNIQUE (tenant_id,project_id,site_id,requester_id,idempotency_key),
    FOREIGN KEY (tenant_id,project_id,site_id,host_id) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships
);

CREATE INDEX cmp_kvm_host_capacity_holds ON cmp_kvm_placements
    (tenant_id,project_id,site_id,host_id,expires_at) WHERE released_at IS NULL AND expired_at IS NULL;

CREATE TABLE cmp_kvm_events (
    event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    host_id text NOT NULL,
    actor_id text NOT NULL,
    placement_id text REFERENCES cmp_kvm_placements,
    action text NOT NULL CHECK (action IN ('register','drain','resume','rebind','reserve','release')),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id,project_id,site_id,host_id) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES cmp_principals,
    CHECK ((action IN ('reserve','release'))=(placement_id IS NOT NULL))
);
