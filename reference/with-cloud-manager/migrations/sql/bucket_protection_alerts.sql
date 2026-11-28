, accounts AS MATERIALIZED (
    SELECT account_id,rule,
        bool_or(COALESCE(state IN ('unconfigured','disabled','suspended'),false)) AS has_finding,
        bool_and(COALESCE(state IN ('aes256','sm4','kms','enabled'),false)) AS all_protected
    FROM observations GROUP BY account_id,rule
), resolved AS (
    UPDATE cmp_project_alerts AS a
    SET resolved_at=GREATEST(clock_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
    WHERE (a.tenant_id,a.project_id)=($1,$2) AND a.resolved_at IS NULL
        AND a.rule IN ('bucket_encryption_unconfigured','bucket_versioning_inactive')
        AND NOT EXISTS (SELECT 1 FROM accounts AS current
            WHERE (current.account_id,current.rule)=(a.account_id,a.rule) AND NOT current.all_protected)
)
INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status)
SELECT $1,$2,a.rule,a.account_id,'object-bucket',a.account_id,'global',
    CASE WHEN a.rule='bucket_encryption_unconfigured' THEN 'encryption_unconfigured' ELSE 'versioning_inactive' END
FROM accounts AS a WHERE a.has_finding
ON CONFLICT (tenant_id,project_id,rule,subject_id) WHERE resolved_at IS NULL DO NOTHING;
