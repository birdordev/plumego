LOCK TABLE schedules, mq_tasks IN SHARE ROW EXCLUSIVE MODE;

CREATE TABLE scheduler_tenant_usage (
    tenant_id text PRIMARY KEY,
    pending_runs bigint NOT NULL DEFAULT 0 CHECK (pending_runs >= 0),
    pending_bytes bigint NOT NULL DEFAULT 0 CHECK (pending_bytes >= 0),
    schedule_count bigint NOT NULL DEFAULT 0 CHECK (schedule_count >= 0),
    max_pending_runs bigint NOT NULL DEFAULT 10000 CHECK (max_pending_runs >= 0),
    max_pending_bytes bigint NOT NULL DEFAULT 67108864 CHECK (max_pending_bytes >= 0),
    max_schedules bigint NOT NULL DEFAULT 1000 CHECK (max_schedules >= 0)
);

INSERT INTO scheduler_tenant_usage (tenant_id, pending_runs, pending_bytes, schedule_count)
SELECT t.tenant_id, COALESCE(q.runs, 0), COALESCE(q.bytes, 0), COALESCE(s.definitions, 0)
FROM (SELECT tenant_id FROM mq_tasks UNION SELECT tenant_id FROM schedules) t
LEFT JOIN (SELECT tenant_id, COUNT(*) AS runs, SUM(COALESCE(octet_length(payload), 0)) AS bytes
           FROM mq_tasks WHERE status IN ('queued', 'leased') GROUP BY tenant_id) q USING (tenant_id)
LEFT JOIN (SELECT tenant_id, COUNT(*) AS definitions FROM schedules GROUP BY tenant_id) s USING (tenant_id);

CREATE FUNCTION scheduler_apply_usage(p_tenant text, p_runs bigint, p_bytes bigint, p_schedules bigint)
RETURNS void LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF p_runs = 0 AND p_bytes = 0 AND p_schedules = 0 THEN
        RETURN;
    END IF;
    INSERT INTO scheduler_tenant_usage (tenant_id) VALUES (p_tenant) ON CONFLICT DO NOTHING;
    UPDATE scheduler_tenant_usage
    SET pending_runs = pending_runs + p_runs,
        pending_bytes = pending_bytes + p_bytes,
        schedule_count = schedule_count + p_schedules
    WHERE tenant_id = p_tenant
      AND (p_runs <= 0 OR pending_runs + p_runs <= max_pending_runs)
      AND (p_bytes <= 0 OR pending_bytes + p_bytes <= max_pending_bytes)
      AND (p_schedules <= 0 OR schedule_count + p_schedules <= max_schedules);
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = 'P5101', MESSAGE = 'tenant capacity exceeded';
    END IF;
END;
$$;

CREATE FUNCTION scheduler_account_task() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    tenant text;
    runs bigint := 0;
    bytes bigint := 0;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.tenant_id <> OLD.tenant_id THEN
        RAISE EXCEPTION 'task tenant is immutable';
    END IF;
    IF TG_OP <> 'INSERT' THEN
        tenant := OLD.tenant_id;
        IF OLD.status IN ('queued', 'leased') THEN
            runs := -1;
            bytes := -COALESCE(octet_length(OLD.payload), 0);
        END IF;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        tenant := NEW.tenant_id;
        IF NEW.status IN ('queued', 'leased') THEN
            runs := runs + 1;
            bytes := bytes + COALESCE(octet_length(NEW.payload), 0);
        END IF;
    END IF;
    PERFORM scheduler_apply_usage(tenant, runs, bytes, 0);
    RETURN NULL;
END;
$$;

CREATE TRIGGER scheduler_account_task
    AFTER INSERT OR DELETE OR UPDATE OF status, tenant_id, payload ON mq_tasks
    FOR EACH ROW EXECUTE FUNCTION scheduler_account_task();

CREATE FUNCTION scheduler_account_definition() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM scheduler_apply_usage(NEW.tenant_id, 0, 0, 1);
    ELSIF TG_OP = 'DELETE' THEN
        PERFORM scheduler_apply_usage(OLD.tenant_id, 0, 0, -1);
    ELSIF NEW.tenant_id <> OLD.tenant_id THEN
        RAISE EXCEPTION 'schedule tenant is immutable';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER scheduler_account_definition
    AFTER INSERT OR DELETE OR UPDATE OF tenant_id ON schedules
    FOR EACH ROW EXECUTE FUNCTION scheduler_account_definition();
