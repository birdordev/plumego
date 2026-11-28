ALTER TABLE cmp_instance_operations ADD COLUMN resource_kind text NOT NULL DEFAULT 'instance';
ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operations_action_check;
ALTER TABLE cmp_instance_operations DROP CONSTRAINT cmp_instance_operation_target;
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_operation_kind_action CHECK (
    (resource_kind='instance' AND action IN ('start','stop','reboot','delete','create','attach-volume','detach-volume'))
    OR (resource_kind='network' AND action='create-network')
    OR (resource_kind='subnet' AND action='create-subnet')
);
ALTER TABLE cmp_instance_operations ADD CONSTRAINT cmp_instance_operation_target CHECK (
    (scope_id IS NOT NULL AND external_id IS NOT NULL) OR (action IN ('create','create-network','create-subnet')
    AND status<>'succeeded' AND scope_id IS NULL AND external_id IS NULL)
);
DROP INDEX cmp_instance_creation_asset;
CREATE UNIQUE INDEX cmp_resource_creation_asset ON cmp_instance_operations (scope_id,external_id)
    WHERE action IN ('create','create-network','create-subnet') AND scope_id IS NOT NULL;

ALTER TABLE cmp_instance_approval_rules DROP CONSTRAINT cmp_instance_approval_rules_action_check;
ALTER TABLE cmp_instance_approval_rules ADD CONSTRAINT cmp_instance_approval_rules_action_check
    CHECK (action IN ('start','stop','reboot','create','attach-volume','create-network','create-subnet'));

CREATE TABLE cmp_network_intents (
    operation_id text PRIMARY KEY REFERENCES cmp_instance_operations,
    launch_token text NOT NULL UNIQUE,
    name text NOT NULL,
    cidr cidr,
    parent_scope_id bigint,
    parent_id text,
    parent_revision bigint NOT NULL DEFAULT 0,
    provider_resource_id text NOT NULL DEFAULT '',
    observation_reviewer text NOT NULL DEFAULT '',
    observation_membership_revision bigint NOT NULL DEFAULT 0,
    observation_credential_revision bigint NOT NULL DEFAULT 0,
    FOREIGN KEY (parent_scope_id,parent_id) REFERENCES cmp_inventory_objects,
    CHECK ((parent_scope_id IS NULL AND parent_id IS NULL AND parent_revision=0) OR
           (parent_scope_id IS NOT NULL AND parent_id IS NOT NULL AND parent_revision>0 AND cidr IS NOT NULL)),
    CHECK ((observation_reviewer='' AND observation_membership_revision=0 AND observation_credential_revision=0) OR
           (observation_reviewer<>'' AND observation_membership_revision>0 AND observation_credential_revision>0)),
    CHECK (cidr IS NULL OR family(cidr)=4)
);
CREATE INDEX cmp_network_intent_parent ON cmp_network_intents (parent_scope_id) WHERE parent_scope_id IS NOT NULL;

ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT cmp_operation_event_outbox_event_type_check;
ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT cmp_operation_event_outbox_event_type_check
    CHECK (event_type ~ '^(instance|network|subnet)\.operation\.(succeeded|failed|needs_review)$');

CREATE OR REPLACE FUNCTION cmp_record_operation_delivery() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    delivery_id uuid;
    topic text;
BEGIN
    delivery_id := gen_random_uuid();
    SELECT resource_kind || '.operation.' || NEW.status INTO STRICT topic
    FROM cmp_instance_operations WHERE operation_id=NEW.operation_id;
    INSERT INTO cmp_operation_event_outbox (sink_id,sequence,public_id,event_type,body)
    SELECT sink.sink_id,NEW.event_id,delivery_id,topic,convert_to(jsonb_strip_nulls(jsonb_build_object(
        'id',delivery_id,'sequence',NEW.event_id::text,'schema_version',1,
        'type',topic,'occurred_at',NEW.recorded_at,
        'tenant',op.tenant_id,'project',op.project_id,'operation_id',op.operation_id,
        'action',op.action,'status',NEW.status,'account',op.account_id,'region',op.region,
        'asset_id',op.external_id,'volume_id',op.volume_id,'reason',op.outcome_reason,'resource_kind',op.resource_kind))::text,'UTF8')
    FROM cmp_instance_operations AS op
    JOIN LATERAL (SELECT sink_id FROM cmp_operation_event_sinks AS s
        WHERE (s.tenant_id,s.project_id)=(op.tenant_id,op.project_id) AND s.enabled FOR SHARE) AS sink ON true
    WHERE op.operation_id=NEW.operation_id;
    RETURN NEW;
END;
$$;
