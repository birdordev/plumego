ALTER TABLE cmp_kvm_placements
    ADD COLUMN boot_volume_retained boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT cmp_kvm_retained_boot_volume_check CHECK (
        NOT boot_volume_retained OR (consumed_at IS NOT NULL AND released_at IS NOT NULL));

DROP INDEX cmp_kvm_active_boot_volume;
CREATE UNIQUE INDEX cmp_kvm_active_boot_volume ON cmp_kvm_placements
    (tenant_id,project_id,site_id,host_id,storage_pool,boot_volume)
    WHERE consumed_at IS NOT NULL AND (released_at IS NULL OR boot_volume_retained);

ALTER TABLE cmp_kvm_vms
    ADD COLUMN retire_requester_id text NOT NULL DEFAULT '',
    ADD COLUMN retire_requester_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN retire_reviewer_id text NOT NULL DEFAULT '',
    ADD COLUMN retire_reviewer_revision bigint NOT NULL DEFAULT 0,
    DROP CONSTRAINT cmp_kvm_vms_state_check,
    DROP CONSTRAINT cmp_kvm_vms_shutdown_request_check,
    DROP CONSTRAINT cmp_kvm_vms_shutdown_review_check,
    ADD CONSTRAINT cmp_kvm_vms_state_check CHECK (state IN
        ('awaiting_approval','rejected','queued','defining','defined','starting','running',
         'shutdown_requested','shutdown_queued','stopping','stopped',
         'retire_requested','retire_queued','undefining','retired')),
    ADD CONSTRAINT cmp_kvm_vms_shutdown_request_check CHECK (
        (state IN ('shutdown_requested','shutdown_queued','stopping','stopped','retire_requested','retire_queued','undefining','retired')
            AND shutdown_requester_id<>'' AND shutdown_requester_revision>0)
        OR (state IN ('awaiting_approval','rejected','queued','defining','defined','starting','running')
            AND shutdown_requester_id='' AND shutdown_requester_revision=0)),
    ADD CONSTRAINT cmp_kvm_vms_shutdown_review_check CHECK (
        (state IN ('shutdown_queued','stopping','stopped','retire_requested','retire_queued','undefining','retired')
            AND shutdown_reviewer_id<>'' AND shutdown_reviewer_revision>0
            AND shutdown_reviewer_id<>shutdown_requester_id)
        OR (state IN ('awaiting_approval','rejected','queued','defining','defined','starting','running','shutdown_requested')
            AND shutdown_reviewer_id='' AND shutdown_reviewer_revision=0)),
    ADD CONSTRAINT cmp_kvm_vms_retire_request_check CHECK (
        (state IN ('retire_requested','retire_queued','undefining','retired')
            AND retire_requester_id<>'' AND retire_requester_revision>0)
        OR (state NOT IN ('retire_requested','retire_queued','undefining','retired')
            AND retire_requester_id='' AND retire_requester_revision=0)),
    ADD CONSTRAINT cmp_kvm_vms_retire_review_check CHECK (
        (state IN ('retire_queued','undefining','retired') AND retire_reviewer_id<>'' AND retire_reviewer_revision>0
            AND retire_reviewer_id<>retire_requester_id)
        OR (state NOT IN ('retire_queued','undefining','retired') AND retire_reviewer_id='' AND retire_reviewer_revision=0));

ALTER TABLE cmp_kvm_events DROP CONSTRAINT cmp_kvm_events_action_check,
    ADD CONSTRAINT cmp_kvm_events_action_check CHECK (action IN
        ('register','drain','resume','rebind','reserve','release','vm_submit','vm_approve','vm_reject',
         'vm_define','vm_defined','vm_start','vm_running','vm_shutdown_request','vm_shutdown_approve',
         'vm_shutdown_reject','vm_shutdown','vm_stopped','vm_retire_request','vm_retire_approve',
         'vm_retire_reject','vm_undefine','vm_retired'));
