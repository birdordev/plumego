WITH budget_state AS MATERIALIZED (
    SELECT budget_id::text AS subject_id,state,
        CASE state WHEN 'warning' THEN 'billing_budget_warning'
                   WHEN 'exceeded' THEN 'billing_budget_exceeded' END AS desired_rule
    FROM cmp_billing_project_budget_status
    WHERE (tenant_id,project_id)=($1,$2)
), resolved AS (
    UPDATE cmp_project_alerts AS a
    SET resolved_at=GREATEST(clock_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
    FROM budget_state AS b
    WHERE (a.tenant_id,a.project_id)=($1,$2) AND a.subject_id=b.subject_id
        AND a.resource_kind='billing-project-budget' AND a.resolved_at IS NULL
        AND b.state<>'unavailable' AND a.rule IS DISTINCT FROM b.desired_rule
    RETURNING a.subject_id
)
INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status,opened_at)
SELECT $1,$2,b.desired_rule,b.subject_id,'billing-project-budget','','',b.state,clock_timestamp()
FROM budget_state AS b
WHERE b.desired_rule IS NOT NULL AND (
    EXISTS (SELECT 1 FROM resolved AS r WHERE r.subject_id=b.subject_id)
    OR NOT EXISTS (
        SELECT 1 FROM cmp_project_alerts AS a
        WHERE (a.tenant_id,a.project_id)=($1,$2) AND a.subject_id=b.subject_id
            AND a.resource_kind='billing-project-budget' AND a.resolved_at IS NULL
    )
)
ON CONFLICT DO NOTHING;
