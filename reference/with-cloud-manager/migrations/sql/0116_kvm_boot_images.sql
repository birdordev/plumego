CREATE TABLE cmp_kvm_boot_images (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    site_id text NOT NULL,
    image_id text NOT NULL,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 34359738368),
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,project_id,site_id,image_id),
    FOREIGN KEY (tenant_id,project_id,created_by) REFERENCES cmp_memberships
);

ALTER TABLE cmp_kvm_placements
    ADD COLUMN image_id text,
    ADD CONSTRAINT cmp_kvm_placement_image_fk FOREIGN KEY (tenant_id,project_id,site_id,image_id) REFERENCES cmp_kvm_boot_images,
    ADD CONSTRAINT cmp_kvm_placement_image_bound CHECK (image_id IS NULL OR consumed_at IS NOT NULL);

ALTER TABLE cmp_kvm_vms
    DROP CONSTRAINT cmp_kvm_vms_state_check,
    DROP CONSTRAINT cmp_kvm_vms_observation_check,
    DROP CONSTRAINT cmp_kvm_vms_shutdown_request_check,
    DROP CONSTRAINT cmp_kvm_vms_shutdown_review_check,
    ADD CONSTRAINT cmp_kvm_vms_state_check CHECK (state IN
        ('awaiting_approval','rejected','queued','staging','staged','defining','defined','starting','running',
         'shutdown_requested','shutdown_queued','stopping','stopped','retire_requested','retire_queued','undefining','retired')),
    ADD CONSTRAINT cmp_kvm_vms_observation_check CHECK (observation IN ('','absent','defined','running','mismatch','staged')),
    ADD CONSTRAINT cmp_kvm_vms_shutdown_request_check CHECK (
        (state IN ('shutdown_requested','shutdown_queued','stopping','stopped','retire_requested','retire_queued','undefining','retired')
            AND shutdown_requester_id<>'' AND shutdown_requester_revision>0)
        OR (state IN ('awaiting_approval','rejected','queued','staging','staged','defining','defined','starting','running')
            AND shutdown_requester_id='' AND shutdown_requester_revision=0)),
    ADD CONSTRAINT cmp_kvm_vms_shutdown_review_check CHECK (
        (state IN ('shutdown_queued','stopping','stopped','retire_requested','retire_queued','undefining','retired')
            AND shutdown_reviewer_id<>'' AND shutdown_reviewer_revision>0 AND shutdown_reviewer_id<>shutdown_requester_id)
        OR (state IN ('awaiting_approval','rejected','queued','staging','staged','defining','defined','starting','running','shutdown_requested')
            AND shutdown_reviewer_id='' AND shutdown_reviewer_revision=0));

ALTER TABLE cmp_kvm_events DROP CONSTRAINT cmp_kvm_events_action_check,
    ADD CONSTRAINT cmp_kvm_events_action_check CHECK (action IN
        ('register','drain','resume','rebind','reserve','release','vm_submit','vm_approve','vm_reject',
         'vm_stage','vm_staged','vm_define','vm_defined','vm_start','vm_running','vm_shutdown_request','vm_shutdown_approve',
         'vm_shutdown_reject','vm_shutdown','vm_stopped','vm_retire_request','vm_retire_approve',
         'vm_retire_reject','vm_undefine','vm_retired'));
