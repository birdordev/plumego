CREATE TABLE cmp_kvm_fenced_vm_recoveries (
    vm_id uuid NOT NULL REFERENCES cmp_kvm_vms,
    recovery_id text NOT NULL,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    host_id text NOT NULL,
    host_uuid uuid NOT NULL,
    fence_generation bigint NOT NULL CHECK (fence_generation>0),
    vm_revision bigint NOT NULL CHECK (vm_revision>0),
    source_state text NOT NULL CHECK (source_state IN ('staging','staged','defining','defined','starting','stopping','undefining')),
    observed text NOT NULL CHECK (observed IN ('absent','defined','running')),
    evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[0-9a-f]{64}$'),
    second_evidence_sha256 text NOT NULL DEFAULT '',
    requester_id text NOT NULL,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    reviewer_id text,
    reviewer_revision bigint CHECK (reviewer_revision>0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    result_state text NOT NULL DEFAULT '',
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reviewed_at timestamptz,
    PRIMARY KEY (vm_id,recovery_id),
    FOREIGN KEY (tenant_id,project_id,site_id,host_id) REFERENCES cmp_kvm_hosts,
    FOREIGN KEY (tenant_id,project_id,requester_id) REFERENCES cmp_memberships,
    FOREIGN KEY (tenant_id,project_id,reviewer_id) REFERENCES cmp_memberships,
    CHECK (CASE WHEN observed='absent'
           THEN second_evidence_sha256 ~ '^[0-9a-f]{64}$' AND second_evidence_sha256<>evidence_sha256
           ELSE second_evidence_sha256='' END),
    CHECK ((status='pending')=(reviewed_at IS NULL)
           AND (reviewed_at IS NULL)=(reviewer_id IS NULL)
           AND (reviewer_id IS NULL)=(reviewer_revision IS NULL)),
    CHECK (reviewer_id<>requester_id),
    CHECK (CASE WHEN status='approved' THEN result_state IN ('rejected','running','stopped','retired') ELSE result_state='' END)
);

CREATE UNIQUE INDEX cmp_kvm_fenced_vm_recovery_pending ON cmp_kvm_fenced_vm_recoveries (vm_id)
    WHERE status='pending';
CREATE INDEX cmp_kvm_fenced_vm_recovery_recent ON cmp_kvm_fenced_vm_recoveries (vm_id,requested_at DESC,recovery_id);
