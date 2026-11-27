, evidence AS MATERIALIZED (
    SELECT revision::text || '/' || kind AS subject_id,kind,
        bool_or(known AND compliant IS FALSE) AS has_finding,
        bool_and(known AND compliant IS TRUE) AS all_compliant
    FROM assessed GROUP BY revision,kind
), resolved AS (
    UPDATE cmp_project_alerts AS a
    SET resolved_at=GREATEST(clock_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
    WHERE (a.tenant_id,a.project_id,a.rule)=($1,$2,'tag_noncompliant') AND a.resolved_at IS NULL
        AND NOT EXISTS (SELECT 1 FROM evidence AS current
            WHERE current.subject_id=a.subject_id AND NOT current.all_compliant)
)
INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status)
SELECT $1,$2,'tag_noncompliant',e.subject_id,e.kind,'','','noncompliant'
FROM evidence AS e WHERE e.has_finding
ON CONFLICT (tenant_id,project_id,rule,subject_id) WHERE resolved_at IS NULL DO NOTHING;
