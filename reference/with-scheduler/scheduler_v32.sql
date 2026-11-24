CREATE TABLE scheduler_workflow_fanouts (
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    node_id text NOT NULL,
    total integer NOT NULL CHECK (total BETWEEN 1 AND 1000000),
    max_in_flight integer NOT NULL CHECK (max_in_flight BETWEEN 1 AND 256),
    next_ordinal integer NOT NULL DEFAULT 0,
    active integer NOT NULL DEFAULT 0,
    succeeded integer NOT NULL DEFAULT 0,
    failed integer NOT NULL DEFAULT 0,
    canceled integer NOT NULL DEFAULT 0,
    activated boolean NOT NULL DEFAULT false,
    resolved boolean NOT NULL DEFAULT false,
    cancel_requested boolean NOT NULL DEFAULT false,
    PRIMARY KEY (tenant_id, workflow_id, node_id),
    FOREIGN KEY (tenant_id, workflow_id, node_id)
        REFERENCES scheduler_workflow_nodes (tenant_id, workflow_id, node_id),
    CHECK (next_ordinal BETWEEN 0 AND total AND active BETWEEN 0 AND max_in_flight
        AND succeeded >= 0 AND failed >= 0 AND canceled >= 0
        AND next_ordinal = active+succeeded+failed+canceled)
);

CREATE INDEX scheduler_workflow_fanout_expand ON scheduler_workflow_fanouts (tenant_id, workflow_id, node_id)
    WHERE activated AND NOT resolved AND NOT cancel_requested AND next_ordinal < total AND active < max_in_flight;
CREATE INDEX scheduler_workflow_fanout_cancel ON scheduler_workflow_fanouts (tenant_id, workflow_id, node_id)
    WHERE cancel_requested AND NOT resolved;

CREATE TABLE scheduler_workflow_fanout_children (
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    node_id text NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal > 0),
    run_id text NOT NULL UNIQUE REFERENCES scheduled_runs (id),
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','succeeded','failed','canceled')),
    PRIMARY KEY (tenant_id, workflow_id, node_id, ordinal),
    FOREIGN KEY (tenant_id, workflow_id, node_id)
        REFERENCES scheduler_workflow_fanouts (tenant_id, workflow_id, node_id)
);

CREATE INDEX scheduler_workflow_fanout_busy ON scheduler_workflow_fanout_children (tenant_id, workflow_id, node_id, ordinal)
    WHERE state = 'queued';

CREATE OR REPLACE VIEW scheduler_blocked_claims AS
SELECT r.id AS task_id
FROM schedules s JOIN scheduled_runs r ON r.tenant_id = s.tenant_id AND r.schedule_id = s.id
WHERE s.claim_paused
UNION ALL
SELECT task_id FROM mq_task_attempts WHERE finalization_state = 'pending'
UNION ALL
SELECT c.run_id FROM scheduler_workflow_fanouts f JOIN scheduler_workflow_fanout_children c
    USING (tenant_id,workflow_id,node_id)
WHERE f.cancel_requested AND NOT f.resolved AND c.state = 'queued';

-- 调用方须持有流程实例行锁，屏障结果与下游就绪同事务写入。
CREATE FUNCTION scheduler_settle_fanout(p_tenant text,p_flow text,p_node text)
RETURNS void LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    ok boolean;
    current_state text;
BEGIN
    UPDATE scheduler_workflow_fanouts f SET resolved=true
     WHERE f.tenant_id=p_tenant AND f.workflow_id=p_flow AND f.node_id=p_node AND NOT f.resolved
        AND f.active=0 AND (f.cancel_requested OR f.next_ordinal=f.total)
    RETURNING NOT f.cancel_requested AND f.failed=0 AND f.canceled=0 INTO ok;
    IF NOT FOUND THEN RETURN; END IF;
    SELECT n.state INTO current_state FROM scheduler_workflow_nodes n
     WHERE n.tenant_id=p_tenant AND n.workflow_id=p_flow AND n.node_id=p_node;
    PERFORM scheduler_complete_workflow_node(p_tenant,p_flow,p_node,current_state,ok);
END $$;

CREATE OR REPLACE FUNCTION scheduler_advance_workflow() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    flow text;
    node text;
    child_state text;
BEGIN
    SELECT workflow_id, node_id INTO flow, node FROM scheduler_workflow_nodes
      WHERE run_id = NEW.id AND tenant_id = NEW.tenant_id;
    IF FOUND THEN
        PERFORM 1 FROM scheduler_workflows WHERE tenant_id=NEW.tenant_id AND id=flow FOR UPDATE;
        PERFORM scheduler_complete_workflow_node(NEW.tenant_id,flow,node,'queued',NEW.status='done');
        RETURN NEW;
    END IF;

    SELECT workflow_id, node_id INTO flow, node FROM scheduler_workflow_fanout_children
      WHERE run_id=NEW.id AND tenant_id=NEW.tenant_id;
    IF NOT FOUND THEN RETURN NEW; END IF;
    PERFORM 1 FROM scheduler_workflows WHERE tenant_id=NEW.tenant_id AND id=flow FOR UPDATE;
    child_state := CASE NEW.status WHEN 'done' THEN 'succeeded' WHEN 'canceled' THEN 'canceled' ELSE 'failed' END;
    UPDATE scheduler_workflow_fanout_children SET state=child_state
     WHERE run_id=NEW.id AND tenant_id=NEW.tenant_id AND state='queued';
    IF NOT FOUND THEN RETURN NEW; END IF;

    UPDATE scheduler_workflow_fanouts SET active=active-1,
        succeeded=succeeded+(child_state='succeeded')::integer,
        failed=failed+(child_state='failed')::integer,
        canceled=canceled+(child_state='canceled')::integer
     WHERE tenant_id=NEW.tenant_id AND workflow_id=flow AND node_id=node;
    PERFORM scheduler_settle_fanout(NEW.tenant_id,flow,node);
    RETURN NEW;
END $$;
