ALTER TABLE cmp_operation_event_outbox
    DROP CONSTRAINT cmp_event_outbox_source,
    ADD CONSTRAINT cmp_event_outbox_source CHECK (
        (sequence IS NOT NULL AND alert_id IS NULL AND event_type ~
            '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume|image|load-balancer|dns-zone)\.operation\.(succeeded|failed|needs_review)$') OR
        (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved')));
