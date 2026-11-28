ALTER TABLE cmp_kvm_vms
    ADD COLUMN shutdown_requester_id text NOT NULL DEFAULT '',
    ADD COLUMN shutdown_requester_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN shutdown_reviewer_id text NOT NULL DEFAULT '',
    ADD COLUMN shutdown_reviewer_revision bigint NOT NULL DEFAULT 0,
    DROP CONSTRAINT cmp_kvm_vms_state_check,
    ADD CONSTRAINT cmp_kvm_vms_state_check CHECK (state IN
        ('awaiting_approval','rejected','queued','defining','defined','starting','running',
         'shutdown_requested','shutdown_queued','stopping','stopped')),
    ADD CONSTRAINT cmp_kvm_vms_shutdown_request_check CHECK (
        (state IN ('shutdown_requested','shutdown_queued','stopping','stopped')
            AND shutdown_requester_id<>'' AND shutdown_requester_revision>0)
        OR (state NOT IN ('shutdown_requested','shutdown_queued','stopping','stopped')
            AND shutdown_requester_id='' AND shutdown_requester_revision=0)),
    ADD CONSTRAINT cmp_kvm_vms_shutdown_review_check CHECK (
        (state IN ('shutdown_queued','stopping','stopped')
            AND shutdown_reviewer_id<>'' AND shutdown_reviewer_revision>0
            AND shutdown_reviewer_id<>shutdown_requester_id)
        OR (state NOT IN ('shutdown_queued','stopping','stopped')
            AND shutdown_reviewer_id='' AND shutdown_reviewer_revision=0));

ALTER TABLE cmp_kvm_events DROP CONSTRAINT cmp_kvm_events_action_check,
    ADD CONSTRAINT cmp_kvm_events_action_check CHECK (action IN
        ('register','drain','resume','rebind','reserve','release','vm_submit','vm_approve','vm_reject',
         'vm_define','vm_defined','vm_start','vm_running','vm_shutdown_request','vm_shutdown_approve',
         'vm_shutdown_reject','vm_shutdown','vm_stopped'));
