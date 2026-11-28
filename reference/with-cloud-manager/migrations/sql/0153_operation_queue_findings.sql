SET LOCAL lock_timeout = '2s';

CREATE OR REPLACE VIEW cmp_operation_queue_snapshot AS
SELECT q.tenant_id,q.project_id,q.account_id,q.region,q.provider,
    q.available_at,q.created_at,q.lease_until,q.state,q.operation_id,
    CASE WHEN q.state='anomaly' THEN 'anomaly'
        WHEN timing.overdue THEN 'overdue_recovery' END AS issue,
    CASE WHEN q.state='anomaly' THEN CASE q.task_status
                WHEN 'dead' THEN 'dead_letter'
                WHEN 'done' THEN 'finished_task'
                ELSE CASE WHEN q.task_status IS NULL THEN 'missing_task' ELSE 'expired_task' END END
        WHEN timing.overdue THEN 'lease_recovery_overdue' END AS issue_reason
FROM (
    SELECT o.tenant_id,o.project_id,o.account_id,o.region,a.provider,
        t.available_at,t.created_at,t.lease_until,
        CASE WHEN o.status IN ('awaiting_approval','needs_review') THEN o.status
            WHEN t.status='leased' AND t.lease_until>=statement_timestamp() THEN 'leased'
            WHEN t.status IS NULL OR t.status NOT IN ('queued','leased') OR t.expires_at<=statement_timestamp() THEN 'anomaly'
            WHEN t.available_at>statement_timestamp() THEN 'deferred'
            WHEN t.status='leased' THEN 'recoverable'
            ELSE 'ready' END AS state,
        o.operation_id,t.status AS task_status
    FROM cmp_instance_operations AS o
    JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
    LEFT JOIN cmp_operation_tasks AS t ON (t.tenant_id,t.id,t.topic)=(o.tenant_id,o.operation_id,'cmp.instance.v1')
    WHERE o.status IN ('awaiting_approval','queued','applying','observing','needs_review')
) AS q
CROSS JOIN LATERAL (
    SELECT q.state='recoverable'
        AND GREATEST(q.available_at,q.created_at,q.lease_until)<=statement_timestamp()-interval '2 minutes' AS overdue
) AS timing;
