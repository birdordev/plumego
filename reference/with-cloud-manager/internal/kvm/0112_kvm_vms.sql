ALTER TABLE cmp_kvm_placements
    ADD COLUMN consumed_at timestamptz,
    ADD COLUMN boot_volume text NOT NULL DEFAULT '',
    ADD CHECK ((consumed_at IS NULL)=(boot_volume='')),
    ADD CHECK (consumed_at IS NULL OR expired_at IS NULL);

CREATE UNIQUE INDEX cmp_kvm_active_boot_volume ON cmp_kvm_placements
    (tenant_id,project_id,site_id,host_id,storage_pool,boot_volume)
    WHERE consumed_at IS NOT NULL AND released_at IS NULL;

CREATE TABLE cmp_kvm_vms (
    vm_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    placement_id text NOT NULL UNIQUE REFERENCES cmp_kvm_placements,
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    reviewer_id text NOT NULL DEFAULT '',
    reviewer_revision bigint NOT NULL DEFAULT 0,
    state text NOT NULL DEFAULT 'awaiting_approval'
        CHECK (state IN ('awaiting_approval','rejected','queued','defining','defined','starting','running')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    observation text NOT NULL DEFAULT ''
        CHECK (observation IN ('','absent','defined','running','mismatch')),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((state='awaiting_approval' AND reviewer_id='' AND reviewer_revision=0)
        OR (state<>'awaiting_approval' AND reviewer_id<>'' AND reviewer_revision>0))
);

ALTER TABLE cmp_kvm_events DROP CONSTRAINT cmp_kvm_events_action_check,
    ADD CONSTRAINT cmp_kvm_events_action_check CHECK (action IN
        ('register','drain','resume','rebind','reserve','release','vm_submit','vm_approve','vm_reject','vm_define','vm_defined','vm_start','vm_running'));
ALTER TABLE cmp_kvm_events DROP CONSTRAINT cmp_kvm_events_check,
    ADD CONSTRAINT cmp_kvm_events_placement_check CHECK
        ((action NOT IN ('register','drain','resume','rebind'))=(placement_id IS NOT NULL));
