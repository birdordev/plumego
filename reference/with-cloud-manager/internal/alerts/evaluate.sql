WITH due AS (
    SELECT tenant_id,project_id FROM cmp_project_alert_checks WHERE next_at<=statement_timestamp()
    ORDER BY next_at,tenant_id,project_id LIMIT 1 FOR UPDATE SKIP LOCKED
), project AS (
    UPDATE cmp_project_alert_checks AS c SET next_at=statement_timestamp()+interval '30 seconds'
    FROM due WHERE (c.tenant_id,c.project_id)=(due.tenant_id,due.project_id)
    RETURNING c.tenant_id,c.project_id
), inventory_status AS (
    SELECT s.schedule_id,s.account_id,s.region,s.kind,s.failures,s.last_outcome,
        GREATEST(s.scheduled_at,i.completed_at,s.last_success_at) AS fresh_at,
        GREATEST(3*s.interval_seconds,900) AS max_age
    FROM project AS p
    JOIN cmp_cloud_accounts AS a USING (tenant_id,project_id)
    JOIN cmp_inventory_schedules AS s USING (tenant_id,account_id)
    LEFT JOIN cmp_inventory_scopes AS i USING (tenant_id,account_id,region,kind)
    WHERE s.enabled AND s.kind<>'instance-idle' AND a.mode IN ('read_only','write_enabled')
), candidates (rule,subject_id,resource_kind,account_id,region,source_status) AS MATERIALIZED (
    SELECT CASE WHEN o.status='needs_review' THEN 'operation_review' ELSE 'operation_delayed' END,
        o.operation_id,o.resource_kind,o.account_id,o.region,o.status
    FROM project AS p JOIN cmp_instance_operations AS o USING (tenant_id,project_id)
    WHERE o.status='needs_review'
       OR (o.status='queued' AND COALESCE(o.updated_at,o.created_at)<=statement_timestamp()-interval '10 minutes')
       OR (o.status IN ('awaiting_approval','applying','observing')
           AND COALESCE(o.updated_at,o.created_at)<=statement_timestamp()-interval '30 minutes')
    UNION ALL
    SELECT 'scan_failures',schedule_id::text,kind,account_id,region,last_outcome
    FROM inventory_status WHERE failures>=3 AND last_outcome IN ('failure','throttled')
    UNION ALL
    SELECT 'scan_stale',schedule_id::text,kind,account_id,region,'stale'
    FROM inventory_status WHERE fresh_at<=statement_timestamp()-max_age*interval '1 second'
), resolved AS (
    UPDATE cmp_project_alerts AS a SET resolved_at=GREATEST(statement_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
    FROM project AS p WHERE (a.tenant_id,a.project_id)=(p.tenant_id,p.project_id) AND a.resolved_at IS NULL
    AND a.rule IN ('operation_review','operation_delayed','scan_failures','scan_stale')
    AND NOT EXISTS (SELECT 1 FROM candidates AS c WHERE (c.rule,c.subject_id)=(a.rule,a.subject_id))
), opened AS (
    INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status)
    SELECT p.tenant_id,p.project_id,c.rule,c.subject_id,c.resource_kind,c.account_id,c.region,c.source_status
    FROM project AS p CROSS JOIN candidates AS c
    ON CONFLICT (tenant_id,project_id,rule,subject_id) WHERE resolved_at IS NULL DO NOTHING
)
SELECT tenant_id,project_id FROM project
