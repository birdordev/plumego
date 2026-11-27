CREATE TABLE cmp_kvm_evacuation_plans (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    source_host text NOT NULL,
    plan_id text NOT NULL,
    source_fence_generation bigint NOT NULL CHECK (source_fence_generation>0),
    host_revision bigint NOT NULL CHECK (host_revision>0),
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    items jsonb NOT NULL CHECK (jsonb_typeof(items)='array' AND jsonb_array_length(items) BETWEEN 1 AND 16),
    status text NOT NULL DEFAULT 'armed' CHECK (status IN ('armed','submitted','cancelled','failed')),
    last_result text NOT NULL DEFAULT '',
    armed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    activated_at timestamptz,
    next_attempt_at timestamptz,
    finished_at timestamptz,
    cancelled_by text,
    batch_id text,
    PRIMARY KEY (tenant_id,project_id,site_id,source_host,plan_id),
    FOREIGN KEY (tenant_id,project_id,site_id,source_host) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,cancelled_by) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,site_id,source_host,batch_id) REFERENCES cmp_kvm_evacuation_batches,
    CHECK ((status='submitted')=(batch_id IS NOT NULL)),
    CHECK ((status='cancelled')=(cancelled_by IS NOT NULL)),
    CHECK ((status='armed')=(finished_at IS NULL)),
    CHECK ((next_attempt_at IS NOT NULL)=(status='armed' AND activated_at IS NOT NULL))
);

CREATE INDEX cmp_kvm_armed_evacuation_plans_host
    ON cmp_kvm_evacuation_plans (tenant_id,project_id,site_id,source_host,source_fence_generation) WHERE status='armed';
CREATE INDEX cmp_kvm_evacuation_plans_due
    ON cmp_kvm_evacuation_plans (next_attempt_at,tenant_id,project_id,site_id,source_host,plan_id)
    WHERE next_attempt_at IS NOT NULL;
