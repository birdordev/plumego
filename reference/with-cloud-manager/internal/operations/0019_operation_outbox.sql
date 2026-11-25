CREATE TABLE cmp_operation_event_sinks (
    sink_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    endpoint_hash bytea NOT NULL CHECK (octet_length(endpoint_hash)=32),
    enabled boolean NOT NULL DEFAULT true,
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);
CREATE INDEX cmp_event_sink_scope ON cmp_operation_event_sinks (tenant_id,project_id) WHERE enabled;
CREATE INDEX cmp_instance_operation_event_sequence ON cmp_instance_operation_events (operation_id,event_id);

CREATE TABLE cmp_operation_event_outbox (
    sink_id text NOT NULL REFERENCES cmp_operation_event_sinks,
    sequence bigint NOT NULL REFERENCES cmp_instance_operation_events(event_id),
    public_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('instance.operation.succeeded','instance.operation.failed','instance.operation.needs_review')),
    body bytea NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 8192),
    available_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    lease_token text,
    lease_until timestamptz,
    delivered_at timestamptz,
    failed_at timestamptz,
    failure text,
    http_status integer CHECK (http_status BETWEEN 100 AND 599),
    PRIMARY KEY (sink_id,sequence),
    UNIQUE (sink_id,public_id),
    CHECK ((lease_token IS NULL)=(lease_until IS NULL)),
    CHECK (delivered_at IS NULL OR failed_at IS NULL)
);
CREATE INDEX cmp_event_outbox_due ON cmp_operation_event_outbox (sink_id,available_at,sequence)
    WHERE delivered_at IS NULL AND failed_at IS NULL;
CREATE INDEX cmp_event_outbox_failed ON cmp_operation_event_outbox (sink_id,failed_at,sequence)
    WHERE failed_at IS NOT NULL;

CREATE FUNCTION cmp_record_operation_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    delivery_id uuid;
    topic text;
BEGIN
    delivery_id := gen_random_uuid();
    topic := 'instance.operation.' || NEW.status;
    INSERT INTO cmp_operation_event_outbox (sink_id,sequence,public_id,event_type,body)
    SELECT sink.sink_id,NEW.event_id,delivery_id,topic,convert_to(jsonb_strip_nulls(jsonb_build_object(
        'id',delivery_id,'sequence',NEW.event_id::text,'schema_version',1,
        'type',topic,'occurred_at',NEW.recorded_at,
        'tenant',op.tenant_id,'project',op.project_id,'operation_id',op.operation_id,
        'action',op.action,'status',NEW.status,'account',op.account_id,'region',op.region,
        'asset_id',op.external_id,'volume_id',op.volume_id,'reason',op.outcome_reason))::text,'UTF8')
    FROM cmp_instance_operations AS op
    JOIN LATERAL (SELECT sink_id FROM cmp_operation_event_sinks AS s
        WHERE (s.tenant_id,s.project_id)=(op.tenant_id,op.project_id) AND s.enabled FOR SHARE) AS sink ON true
    WHERE op.operation_id=NEW.operation_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_instance_operation_delivery
    AFTER INSERT ON cmp_instance_operation_events FOR EACH ROW
    WHEN (NEW.status IN ('succeeded','failed','needs_review'))
    EXECUTE FUNCTION cmp_record_operation_delivery();
