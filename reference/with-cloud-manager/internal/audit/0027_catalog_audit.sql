ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation','catalog'));

CREATE FUNCTION cmp_audit_catalog(e cmp_instance_catalog_events) RETURNS void LANGUAGE sql AS $$
    INSERT INTO cmp_project_audit
        (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,revision,occurred_at)
    SELECT e.tenant_id,e.project_id,'catalog',e.action,e.template_id,'instance-template',v.account_id,v.region,
        e.actor_id,e.revision,e.occurred_at
    FROM cmp_instance_catalog_versions AS v
    WHERE (v.tenant_id,v.project_id,v.template_id,v.version)=(e.tenant_id,e.project_id,e.template_id,e.version);
$$;

CREATE FUNCTION cmp_capture_catalog_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM cmp_audit_catalog(NEW);
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_audit_catalog_capture AFTER INSERT ON cmp_instance_catalog_events
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_catalog_audit();

DO $$
BEGIN
    PERFORM cmp_audit_catalog(e) FROM cmp_instance_catalog_events AS e;
END;
$$;
