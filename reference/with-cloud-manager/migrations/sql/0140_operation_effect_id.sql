CREATE OR REPLACE FUNCTION cmp_record_operation_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    delivery_id uuid := gen_random_uuid();
    topic text;
BEGIN
    SELECT resource_kind || '.operation.' || NEW.status INTO STRICT topic
    FROM cmp_instance_operations WHERE operation_id=NEW.operation_id;
    INSERT INTO cmp_operation_event_outbox (sink_id,sequence,public_id,event_type,body)
    SELECT sink.sink_id,NEW.event_id,delivery_id,topic,convert_to(jsonb_strip_nulls(jsonb_build_object(
        'id',delivery_id,'sequence',NEW.event_id::text,'schema_version',1,
        'type',topic,'occurred_at',NEW.recorded_at,
        'tenant',op.tenant_id,'project',op.project_id,'operation_id',op.operation_id,
        'effect_id',CASE WHEN NEW.status IN ('succeeded','failed') THEN 'cmp-op-final-v1:' || op.operation_id END,
        'action',op.action,'status',NEW.status,'account',op.account_id,'region',op.region,
        'asset_id',op.external_id,'volume_id',op.volume_id,'reason',op.outcome_reason,'resource_kind',op.resource_kind))::text,'UTF8')
    FROM cmp_instance_operations AS op
    JOIN LATERAL (SELECT sink_id FROM cmp_operation_event_sinks AS s
        WHERE (s.tenant_id,s.project_id)=(op.tenant_id,op.project_id) AND s.enabled FOR SHARE) AS sink ON true
    WHERE op.operation_id=NEW.operation_id;
    RETURN NEW;
END;
$$;
