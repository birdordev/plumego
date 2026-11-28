ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operations_action_check;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operations_action_check
    CHECK (action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'));
ALTER TABLE cmp_instance_operations ADD COLUMN volume_scope_id bigint,
    ADD COLUMN volume_id text, ADD COLUMN volume_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN device text NOT NULL DEFAULT '', ADD COLUMN guest_unmounted boolean NOT NULL DEFAULT false,
    ADD FOREIGN KEY (tenant_id,volume_scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    ADD FOREIGN KEY (volume_scope_id,volume_id) REFERENCES cmp_inventory_objects,
    ADD CONSTRAINT cmp_instance_volume_target CHECK (
        (action IN ('attach-volume','detach-volume') AND volume_scope_id IS NOT NULL AND volume_id IS NOT NULL AND volume_revision>0)
        OR (action NOT IN ('attach-volume','detach-volume') AND volume_scope_id IS NULL AND volume_id IS NULL AND volume_revision=0 AND device='')
    ), ADD CHECK (guest_unmounted=(action='detach-volume'));

CREATE UNIQUE INDEX cmp_active_volume_operation ON cmp_instance_operations (volume_scope_id,volume_id)
    WHERE volume_scope_id IS NOT NULL AND status IN ('awaiting_approval','queued','applying','observing','needs_review');

ALTER TABLE cmp_instance_approval_rules DROP CONSTRAINT cmp_instance_approval_rules_action_check;
ALTER TABLE cmp_instance_approval_rules ADD CONSTRAINT cmp_instance_approval_rules_action_check
    CHECK (action IN ('start','stop','reboot','create','attach-volume'));
