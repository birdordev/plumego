SELECT COALESCE(o.status,''), COALESCE(o.resource_kind,''),
       COALESCE(o.action,''), COALESCE(o.account_id,''), COALESCE(o.region,''),
       COALESCE(o.external_id,''), COALESCE(o.volume_id,''),
       COALESCE(exact.public_id::text,replacement.public_id::text,''),
       COALESCE(exact.event_type,replacement.event_type,''),
       COALESCE(original.operation_id,replacement.operation_id,''),
       exact.body, original.recorded_at
FROM jsonb_array_elements($4::jsonb) WITH ORDINALITY AS receipt(data, position)
LEFT JOIN cmp_instance_operations AS o
    ON (o.tenant_id,o.project_id,o.operation_id)=($1,$2,receipt.data->>'operation_id')
LEFT JOIN cmp_operation_event_outbox AS exact
    ON o.operation_id IS NOT NULL AND (exact.sink_id,exact.public_id)=($3,(receipt.data->>'delivery_id')::uuid)
LEFT JOIN cmp_instance_operation_events AS original ON original.event_id=exact.sequence
LEFT JOIN LATERAL (
    SELECT box.public_id,box.event_type,e.operation_id
    FROM cmp_instance_operation_events AS e
    JOIN cmp_operation_event_outbox AS box ON (box.sink_id,box.sequence)=($3,e.event_id)
    WHERE exact.public_id IS NULL AND e.operation_id=o.operation_id
      AND e.status=o.status AND e.status IN ('succeeded','failed')
    ORDER BY e.event_id DESC LIMIT 1
) AS replacement ON true
ORDER BY receipt.position
