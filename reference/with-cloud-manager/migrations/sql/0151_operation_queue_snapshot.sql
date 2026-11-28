CREATE VIEW cmp_operation_queue_snapshot AS
SELECT o.tenant_id,o.project_id,o.account_id,o.region,a.provider,
    t.available_at,t.created_at,t.lease_until,
    CASE WHEN o.status IN ('awaiting_approval','needs_review') THEN o.status
        WHEN t.status='leased' AND t.lease_until>=statement_timestamp() THEN 'leased'
        WHEN t.status IS NULL OR t.status NOT IN ('queued','leased') OR t.expires_at<=statement_timestamp() THEN 'anomaly'
        WHEN t.available_at>statement_timestamp() THEN 'deferred'
        WHEN t.status='leased' THEN 'recoverable'
        ELSE 'ready' END AS state
FROM cmp_instance_operations AS o
JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
LEFT JOIN cmp_operation_tasks AS t ON (t.tenant_id,t.id,t.topic)=(o.tenant_id,o.operation_id,'cmp.instance.v1')
WHERE o.status IN ('awaiting_approval','queued','applying','observing','needs_review');
