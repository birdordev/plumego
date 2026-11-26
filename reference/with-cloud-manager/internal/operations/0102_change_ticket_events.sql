ALTER TABLE cmp_operation_event_outbox
    DROP CONSTRAINT cmp_event_outbox_source,
    ADD CONSTRAINT cmp_event_outbox_source CHECK (
        (sequence IS NOT NULL AND alert_id IS NULL AND (event_type='change.ticket.requested' OR event_type ~
            '^(instance|network|subnet|security-group|security-group-rule|elastic-ip|snapshot|volume|image|load-balancer|dns-zone|kubernetes-cluster)\.operation\.(succeeded|failed|needs_review)$')) OR
        (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved'))) NOT VALID;

CREATE FUNCTION cmp_record_ticket_request() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    delivery_id uuid := gen_random_uuid();
BEGIN
    INSERT INTO cmp_operation_event_outbox (sink_id,sequence,public_id,event_type,body)
    SELECT sink.sink_id,NEW.event_id,delivery_id,'change.ticket.requested',convert_to(jsonb_strip_nulls(jsonb_build_object(
        'id',delivery_id,'sequence',NEW.event_id::text,'schema_version',1,'type','change.ticket.requested',
        'occurred_at',NEW.recorded_at,'tenant',op.tenant_id,'project',op.project_id,'operation_id',op.operation_id,
        'policy_revision',op.ticket_policy_revision::text,'action',op.action,'resource_kind',op.resource_kind,
        'account',op.account_id,'region',op.region,'asset_id',op.external_id))::text,'UTF8')
    FROM cmp_instance_operations AS op
    JOIN LATERAL (SELECT sink_id FROM cmp_operation_event_sinks AS s
        WHERE (s.tenant_id,s.project_id)=(op.tenant_id,op.project_id) AND s.enabled AND s.include_change_tickets FOR SHARE) AS sink ON true
    WHERE op.operation_id=NEW.operation_id AND op.ticket_policy_revision IS NOT NULL;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_change_ticket_delivery AFTER INSERT ON cmp_instance_operation_events FOR EACH ROW
    WHEN (NEW.status='awaiting_approval') EXECUTE FUNCTION cmp_record_ticket_request();

ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation',
                        'catalog','alert','console_access','billing_budget','tag_policy','change_ticket')) NOT VALID;

CREATE FUNCTION cmp_record_ticket_policy_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO cmp_project_audit
        (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,revision,occurred_at)
    VALUES (NEW.tenant_id,NEW.project_id,'change_ticket','publish_policy',NEW.project_id,'project','',
        NEW.published_by,NEW.revision,NEW.published_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_change_ticket_policy_audit AFTER INSERT ON cmp_change_ticket_policies FOR EACH ROW
    EXECUTE FUNCTION cmp_record_ticket_policy_audit();

CREATE FUNCTION cmp_record_ticket_receipt_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO cmp_project_audit
        (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,outcome,revision,occurred_at)
    SELECT op.tenant_id,op.project_id,'change_ticket','record_receipt',op.operation_id,op.resource_kind,op.account_id,op.region,
        policy.verifier_id,NEW.decision,NEW.source_revision,NEW.recorded_at
    FROM cmp_instance_operations AS op JOIN cmp_change_ticket_policies AS policy
    ON (policy.tenant_id,policy.project_id,policy.revision)=(op.tenant_id,op.project_id,op.ticket_policy_revision)
    WHERE op.operation_id=NEW.operation_id;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_change_ticket_receipt_audit AFTER INSERT ON cmp_change_ticket_receipts FOR EACH ROW
    EXECUTE FUNCTION cmp_record_ticket_receipt_audit();
