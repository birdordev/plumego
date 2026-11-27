SET LOCAL lock_timeout = '2s';

CREATE TABLE cmp_power_observation_authority (
    operation_id text PRIMARY KEY,
    reviewer_id text NOT NULL,
    evidence_ref text NOT NULL,
    FOREIGN KEY (operation_id,reviewer_id,evidence_ref)
        REFERENCES cmp_instance_observation_retries (operation_id,reviewer_id,evidence_ref)
);

-- 迁移前的活动修复仅在最新审批唯一时回填。
WITH latest AS (
    SELECT r.operation_id,r.reviewer_id,r.evidence_ref,
        rank() OVER (PARTITION BY r.operation_id ORDER BY r.created_at DESC) AS position,
        count(*) OVER (PARTITION BY r.operation_id,r.created_at) AS candidates
    FROM cmp_instance_observation_retries AS r
    JOIN cmp_instance_operations AS o ON (o.operation_id,o.tenant_id)=(r.operation_id,r.tenant_id)
    WHERE r.repair_queue AND o.action IN ('start','stop','reboot') AND o.resource_kind='instance'
        AND o.status='observing' AND o.outcome_reason='queue_observation_restarted'
)
INSERT INTO cmp_power_observation_authority (operation_id,reviewer_id,evidence_ref)
SELECT operation_id,reviewer_id,evidence_ref FROM latest WHERE position=1 AND candidates=1;
