ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='bare-metal' AND action IN ('power-on-bare-metal','shutdown-bare-metal'))
        OR (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume','retire-instance'))
        OR (resource_kind='network' AND action IN ('create-network','delete-network'))
        OR (resource_kind='subnet' AND action IN ('create-subnet','delete-subnet'))
        OR (resource_kind='security-group' AND action IN ('create-security-group','delete-security-group'))
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action='restore-snapshot')
        OR (resource_kind='cache-instance' AND action='rename-cache-instance')
        OR (resource_kind='database-instance' AND action='rename-database-instance')
        OR (resource_kind='kubernetes-cluster' AND action='scale-workload')
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status','delete-dns-record'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    ) NOT VALID;

ALTER TABLE cmp_cloud_account_events
    DROP CONSTRAINT cmp_cloud_account_events_action_check,
    ADD CONSTRAINT cmp_cloud_account_events_action_check CHECK (
        action IN ('register','rotate','suspend','resume','transfer','share','unshare',
                   'enable-bare-metal-power','disable-bare-metal-power'));

CREATE UNIQUE INDEX cmp_bare_metal_active_uuid ON cmp_instance_operations (external_id)
    WHERE resource_kind='bare-metal' AND status IN ('awaiting_approval','queued','applying','observing','needs_review');

ALTER TABLE cmp_operation_event_outbox
    DROP CONSTRAINT cmp_event_outbox_source,
    ADD CONSTRAINT cmp_event_outbox_source CHECK (
        (sequence IS NOT NULL AND alert_id IS NULL AND (event_type='change.ticket.requested' OR event_type ~
            '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume|image|load-balancer|dns-zone|kubernetes-cluster|database-instance|cache-instance|bare-metal)\.operation\.(succeeded|failed|needs_review)$')) OR
        (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved'))) NOT VALID;
