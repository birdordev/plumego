CREATE VIEW cmp_operation_observation_blocked_claims AS
SELECT t.id AS task_id
FROM cmp_operation_tasks AS t
WHERE NOT EXISTS (
    SELECT 1
    FROM cmp_instance_operations AS o
    WHERE (o.operation_id,o.tenant_id)=(t.id,t.tenant_id)
      AND o.status IN ('applying','observing')
);
