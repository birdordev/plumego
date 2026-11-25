ALTER TABLE cmp_inventory_schedules
    ADD COLUMN scheduled_at timestamptz NOT NULL DEFAULT statement_timestamp();

CREATE TABLE cmp_project_alert_checks (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    next_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (tenant_id,project_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects ON DELETE CASCADE
);
CREATE INDEX cmp_project_alert_checks_due ON cmp_project_alert_checks (next_at,tenant_id,project_id);

CREATE FUNCTION cmp_enqueue_project_alert_checks() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO cmp_project_alert_checks (tenant_id,project_id) VALUES (NEW.tenant_id,NEW.project_id);
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_project_alert_checks_create AFTER INSERT ON cmp_projects
    FOR EACH ROW EXECUTE FUNCTION cmp_enqueue_project_alert_checks();
INSERT INTO cmp_project_alert_checks (tenant_id,project_id) SELECT tenant_id,project_id FROM cmp_projects;

CREATE TABLE cmp_project_alerts (
    alert_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    rule text NOT NULL CHECK (rule IN ('operation_review','operation_delayed','scan_failures','scan_stale')),
    subject_id text NOT NULL,
    resource_kind text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    source_status text NOT NULL,
    opened_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    acknowledged_at timestamptz,
    acknowledged_by text,
    resolved_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,acknowledged_by) REFERENCES cmp_principals,
    CHECK ((acknowledged_at IS NULL)=(acknowledged_by IS NULL)),
    CHECK (acknowledged_at IS NULL OR acknowledged_at >= opened_at),
    CHECK (resolved_at IS NULL OR resolved_at >= COALESCE(acknowledged_at,opened_at))
);
CREATE UNIQUE INDEX cmp_project_alert_active_subject ON cmp_project_alerts (tenant_id,project_id,rule,subject_id)
    WHERE resolved_at IS NULL;
CREATE INDEX cmp_project_alert_recent ON cmp_project_alerts (tenant_id,project_id,opened_at DESC,alert_id DESC);
CREATE INDEX cmp_project_alert_active_recent ON cmp_project_alerts (tenant_id,project_id,opened_at DESC,alert_id DESC)
    WHERE resolved_at IS NULL;
CREATE INDEX cmp_project_alert_kind_recent ON cmp_project_alerts (tenant_id,project_id,rule,opened_at DESC,alert_id DESC);
CREATE INDEX cmp_project_active_operation_updated ON cmp_instance_operations (tenant_id,project_id,status,COALESCE(updated_at,created_at))
    WHERE status IN ('awaiting_approval','queued','applying','observing','needs_review');

ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation','catalog','alert'));

ALTER TABLE cmp_operation_event_sinks ADD COLUMN include_alerts boolean NOT NULL DEFAULT false;

ALTER TABLE cmp_operation_event_outbox
    DROP CONSTRAINT cmp_operation_event_outbox_pkey,
    DROP CONSTRAINT cmp_operation_event_outbox_sink_id_public_id_key,
    DROP CONSTRAINT cmp_operation_event_outbox_event_type_check,
    ALTER COLUMN sequence DROP NOT NULL,
    ADD COLUMN alert_id uuid REFERENCES cmp_project_alerts,
    ADD PRIMARY KEY (sink_id,public_id),
    ADD CONSTRAINT cmp_event_outbox_source CHECK (
        (sequence IS NOT NULL AND alert_id IS NULL AND event_type ~
            '^(instance|network|subnet|security-group)\.operation\.(succeeded|failed|needs_review)$') OR
        (sequence IS NULL AND alert_id IS NOT NULL AND event_type IN ('project.alert.opened','project.alert.resolved')));
CREATE UNIQUE INDEX cmp_event_outbox_operation ON cmp_operation_event_outbox (sink_id,sequence) WHERE sequence IS NOT NULL;
CREATE UNIQUE INDEX cmp_event_outbox_alert ON cmp_operation_event_outbox (sink_id,alert_id,event_type) WHERE alert_id IS NOT NULL;

CREATE FUNCTION cmp_capture_alert_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    change text;
    change_time timestamptz;
    actor text;
    delivery_id uuid;
    topic text;
BEGIN
    IF TG_OP='INSERT' THEN
        change := 'opened';
        change_time := NEW.opened_at;
    ELSIF OLD.resolved_at IS NULL AND NEW.resolved_at IS NOT NULL THEN
        change := 'resolved';
        change_time := NEW.resolved_at;
    ELSIF OLD.acknowledged_at IS NULL AND NEW.acknowledged_at IS NOT NULL THEN
        change := 'acknowledged';
        change_time := NEW.acknowledged_at;
        actor := NEW.acknowledged_by;
    ELSE
        RETURN NEW;
    END IF;
    INSERT INTO cmp_project_audit (tenant_id,project_id,category,action,subject_id,resource_kind,
                                   account_id,region,actor_id,outcome,revision,occurred_at)
    VALUES (NEW.tenant_id,NEW.project_id,'alert',change,NEW.alert_id::text,NEW.resource_kind,
            NEW.account_id,NEW.region,actor,NEW.rule,NEW.revision,change_time);
    IF change IN ('opened','resolved') THEN
        delivery_id := gen_random_uuid();
        topic := 'project.alert.' || change;
        INSERT INTO cmp_operation_event_outbox (sink_id,public_id,event_type,alert_id,body)
        SELECT sink.sink_id,delivery_id,topic,NEW.alert_id,convert_to(jsonb_build_object(
            'id',delivery_id,'schema_version',1,'type',topic,'occurred_at',change_time,
            'tenant',NEW.tenant_id,'project',NEW.project_id,'alert_id',NEW.alert_id,'revision',NEW.revision,
            'rule',NEW.rule,'subject_id',NEW.subject_id,'resource_kind',NEW.resource_kind,
            'account',NEW.account_id,'region',NEW.region,'source_status',NEW.source_status)::text,'UTF8')
        FROM (SELECT sink_id FROM cmp_operation_event_sinks
              WHERE (tenant_id,project_id)=(NEW.tenant_id,NEW.project_id) AND enabled AND include_alerts FOR SHARE) AS sink;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_project_alert_change AFTER INSERT OR UPDATE ON cmp_project_alerts
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_alert_change();
