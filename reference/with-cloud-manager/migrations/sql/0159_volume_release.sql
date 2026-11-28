SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_instance_operations
    DROP CONSTRAINT cmp_operation_kind_action,
    ADD CONSTRAINT cmp_operation_kind_action CHECK (
        (resource_kind='bare-metal' AND action IN ('power-on-bare-metal','shutdown-bare-metal','stage-bare-metal-pxe','cancel-bare-metal-pxe'))
        OR (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume','retire-instance'))
        OR (resource_kind='network' AND action IN ('create-network','delete-network'))
        OR (resource_kind='subnet' AND action IN ('create-subnet','delete-subnet'))
        OR (resource_kind='security-group' AND action IN ('create-security-group','delete-security-group'))
        OR (resource_kind='security-group-rule' AND action IN ('create-security-group-rule','revoke-security-group-rule'))
        OR (resource_kind='elastic-ip' AND action IN ('create-elastic-ip','release-elastic-ip','bind-elastic-ip','unbind-elastic-ip'))
        OR (resource_kind='snapshot' AND action IN ('create-snapshot','delete-snapshot','rollback-snapshot'))
        OR (resource_kind='image' AND action IN ('create-image','copy-image','delete-image'))
        OR (resource_kind='volume' AND action IN ('restore-snapshot','resize-volume','create-volume','release-volume'))
        OR (resource_kind='cache-instance' AND action='rename-cache-instance')
        OR (resource_kind='database-instance' AND action='rename-database-instance')
        OR (resource_kind='kubernetes-cluster' AND action='scale-workload')
        OR (resource_kind='dns-zone' AND action IN ('create-dns-record','set-dns-record-status','delete-dns-record'))
        OR (resource_kind='load-balancer' AND action IN ('rename-load-balancer','rename-load-balancer-listener','rename-load-balancer-target-group','set-load-balancer-backend-weight','detach-load-balancer-backend','attach-load-balancer-backend'))
    ) NOT VALID;

ALTER TABLE cmp_network_intents
    ADD COLUMN volume_release_size_gib integer NOT NULL DEFAULT 0 CHECK (volume_release_size_gib >= 0);
