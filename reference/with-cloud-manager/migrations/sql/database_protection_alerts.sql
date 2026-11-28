, candidates AS MATERIALIZED (
    SELECT account||'/'||region||'/'||id AS subject_id,account,region,inventory_available,
        CASE WHEN schedule_kind='database-backups' THEN 'database_backup_coverage'
             ELSE 'database_restore_coverage' END AS rule,
        CASE WHEN schedule_kind='database-backups' THEN backup_status ELSE restore_status END AS status
    FROM posture
), resolved AS (
    UPDATE cmp_project_alerts AS a
    SET resolved_at=GREATEST(clock_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
    WHERE (a.tenant_id,a.project_id)=($1,$2) AND a.resolved_at IS NULL
        AND a.rule IN ('database_backup_coverage','database_restore_coverage')
        AND NOT EXISTS (SELECT 1 FROM candidates AS c
            WHERE (c.rule,c.subject_id)=(a.rule,a.subject_id)
              AND NOT (c.inventory_available AND c.status IN ('ready','unsupported')))
)
INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status)
SELECT $1,$2,c.rule,c.subject_id,'database-instance',c.account,c.region,c.status
FROM candidates AS c WHERE c.status IN ('missing','outdated')
ON CONFLICT (tenant_id,project_id,rule,subject_id) WHERE resolved_at IS NULL DO NOTHING;
