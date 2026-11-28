CREATE TABLE cmp_project_audit (
    audit_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    category text NOT NULL CHECK (category IN ('account','ownership','operation','approval','resolution','observation')),
    action text NOT NULL,
    subject_id text NOT NULL,
    resource_kind text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL DEFAULT '',
    actor_id text,
    related_project text,
    outcome text NOT NULL DEFAULT '',
    revision bigint NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL,
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES cmp_principals
);

CREATE INDEX cmp_project_audit_recent
    ON cmp_project_audit (tenant_id,project_id,occurred_at DESC,audit_id DESC);
CREATE INDEX cmp_project_audit_category_recent
    ON cmp_project_audit (tenant_id,project_id,category,occurred_at DESC,audit_id DESC);
CREATE INDEX cmp_project_audit_subject
    ON cmp_project_audit (tenant_id,project_id,subject_id,occurred_at DESC,audit_id DESC);

CREATE FUNCTION cmp_audit_account(e cmp_cloud_account_events) RETURNS void LANGUAGE sql AS $$
    INSERT INTO cmp_project_audit
        (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,related_project,revision,occurred_at)
    SELECT e.tenant_id,p.project_id,'account',e.action,e.account_id,'cloud-account',e.account_id,e.actor_id,
           CASE WHEN p.project_id=e.project_id THEN NULLIF(e.target_project,e.project_id) ELSE e.project_id END,
           e.revision,e.created_at
    FROM cmp_projects AS p
    WHERE p.tenant_id=e.tenant_id AND p.project_id IN (e.project_id,e.target_project);
$$;

CREATE FUNCTION cmp_audit_ownership(e cmp_asset_ownership_events) RETURNS void LANGUAGE sql AS $$
    INSERT INTO cmp_project_audit
        (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,related_project,revision,occurred_at)
    SELECT e.tenant_id,p.project_id,'ownership',
           CASE WHEN e.from_project=e.to_project THEN 'assign' ELSE 'transfer' END,
           e.external_id,s.kind,s.account_id,s.region,e.actor_id,
           CASE WHEN p.project_id=e.from_project THEN NULLIF(e.to_project,e.from_project) ELSE e.from_project END,
           e.revision,e.created_at
    FROM cmp_inventory_scopes AS s
    JOIN cmp_projects AS p ON p.tenant_id=e.tenant_id AND p.project_id IN (e.from_project,e.to_project)
    WHERE (s.tenant_id,s.scope_id)=(e.tenant_id,e.scope_id);
$$;

CREATE FUNCTION cmp_audit_workflow(operation_key text, source_tenant text, event_category text, event_outcome text,
                                   event_actor text, event_time timestamptz, initial_event boolean)
RETURNS void LANGUAGE sql AS $$
    INSERT INTO cmp_project_audit
        (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,outcome,occurred_at)
    SELECT op.tenant_id,op.project_id,event_category,op.action,op.operation_id,op.resource_kind,op.account_id,op.region,
           CASE WHEN initial_event THEN op.principal_id ELSE event_actor END,event_outcome,event_time
    FROM cmp_instance_operations AS op
    WHERE op.operation_id=operation_key AND (source_tenant IS NULL OR op.tenant_id=source_tenant);
$$;

CREATE FUNCTION cmp_audit_operation(e cmp_instance_operation_events) RETURNS void LANGUAGE sql AS $$
    SELECT cmp_audit_workflow(e.operation_id,NULL,'operation',e.status,NULL,e.recorded_at,
        NOT EXISTS (SELECT 1 FROM cmp_instance_operation_events
                    WHERE operation_id=e.operation_id AND event_id<e.event_id));
$$;

CREATE FUNCTION cmp_capture_project_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    CASE TG_TABLE_NAME
    WHEN 'cmp_cloud_account_events' THEN
        PERFORM cmp_audit_account(NEW);
    WHEN 'cmp_asset_ownership_events' THEN
        PERFORM cmp_audit_ownership(NEW);
    WHEN 'cmp_instance_operation_approvals' THEN
        PERFORM cmp_audit_workflow(NEW.operation_id,NEW.tenant_id,'approval',NEW.decision,NEW.reviewer_id,NEW.decided_at,false);
    WHEN 'cmp_instance_operation_resolutions' THEN
        PERFORM cmp_audit_workflow(NEW.operation_id,NEW.tenant_id,'resolution',NEW.decision,NEW.reviewer_id,NEW.reviewed_at,false);
    WHEN 'cmp_instance_observation_retries' THEN
        PERFORM cmp_audit_workflow(NEW.operation_id,NEW.tenant_id,'observation','restarted',NEW.reviewer_id,NEW.created_at,false);
    WHEN 'cmp_instance_operation_events' THEN
        PERFORM cmp_audit_operation(NEW);
    ELSE
        RAISE EXCEPTION 'unsupported project audit source';
    END CASE;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_audit_account_capture AFTER INSERT ON cmp_cloud_account_events
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_project_audit();
CREATE TRIGGER cmp_audit_ownership_capture AFTER INSERT ON cmp_asset_ownership_events
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_project_audit();
CREATE TRIGGER cmp_audit_approval_capture AFTER INSERT ON cmp_instance_operation_approvals
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_project_audit();
CREATE TRIGGER cmp_audit_resolution_capture AFTER INSERT ON cmp_instance_operation_resolutions
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_project_audit();
CREATE TRIGGER cmp_audit_observation_capture AFTER INSERT ON cmp_instance_observation_retries
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_project_audit();
CREATE TRIGGER cmp_audit_operation_capture AFTER INSERT ON cmp_instance_operation_events
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_project_audit();

DO $$
BEGIN
    PERFORM cmp_audit_account(e) FROM cmp_cloud_account_events AS e;
    PERFORM cmp_audit_ownership(e) FROM cmp_asset_ownership_events AS e;
    PERFORM cmp_audit_workflow(d.operation_id,d.tenant_id,'approval',d.decision,d.reviewer_id,d.decided_at,false)
      FROM cmp_instance_operation_approvals AS d;
    PERFORM cmp_audit_workflow(d.operation_id,d.tenant_id,'resolution',d.decision,d.reviewer_id,d.reviewed_at,false)
      FROM cmp_instance_operation_resolutions AS d;
    PERFORM cmp_audit_workflow(d.operation_id,d.tenant_id,'observation','restarted',d.reviewer_id,d.created_at,false)
      FROM cmp_instance_observation_retries AS d;
    PERFORM cmp_audit_operation(e) FROM cmp_instance_operation_events AS e;
END;
$$;
