SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_project_alerts DROP CONSTRAINT cmp_project_alerts_rule_check,
    ADD CONSTRAINT cmp_project_alerts_rule_check CHECK (rule IN (
        'operation_review','operation_delayed','scan_failures','scan_stale','billing_budget_warning','billing_budget_exceeded',
        'bucket_encryption_unconfigured','bucket_versioning_inactive','tag_noncompliant',
        'operation_queue_recovery','operation_queue_anomaly'));
