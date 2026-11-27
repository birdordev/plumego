WITH seen AS MATERIALIZED (
    SELECT DISTINCT value->>'operation_id' AS operation_id
    FROM jsonb_array_elements($6::jsonb)
)
SELECT o.operation_id, e.status, e.recorded_at, box.public_id::text,
       CASE WHEN box.delivered_at IS NOT NULL THEN 'delivered'
            WHEN box.failed_at IS NOT NULL THEN 'failed'
            WHEN box.lease_until > transaction_timestamp() THEN 'leased'
            ELSE 'pending' END
FROM cmp_instance_operation_events AS e
JOIN cmp_operation_event_outbox AS box ON (box.sink_id,box.sequence)=($3,e.event_id)
JOIN cmp_instance_operations AS o ON (o.tenant_id,o.project_id,o.operation_id)=($1,$2,e.operation_id)
WHERE e.status IN ('succeeded','failed') AND e.recorded_at >= $4 AND e.recorded_at < $5
  AND NOT EXISTS (SELECT 1 FROM seen WHERE seen.operation_id=e.operation_id)
ORDER BY e.recorded_at,e.event_id
LIMIT $7
