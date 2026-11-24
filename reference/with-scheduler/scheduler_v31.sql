ALTER TABLE scheduler_workflow_nodes DROP CONSTRAINT scheduler_workflow_nodes_state_check;
ALTER TABLE scheduler_workflow_nodes ADD CHECK (state IN ('blocked','ready','queued','waiting','succeeded','failed','skipped'));

CREATE TABLE scheduler_workflow_waits (
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    node_id text NOT NULL,
    signal_key text NOT NULL,
    signal_actor text NOT NULL,
    deadline timestamptz NOT NULL,
    signal_at timestamptz,
    activated_at timestamptz,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id, workflow_id, node_id),
    UNIQUE (tenant_id, workflow_id, signal_key),
    FOREIGN KEY (tenant_id, workflow_id, node_id)
        REFERENCES scheduler_workflow_nodes (tenant_id, workflow_id, node_id),
    CHECK (signal_at IS NULL OR signal_at < deadline)
);

CREATE INDEX scheduler_workflow_wait_due ON scheduler_workflow_waits (deadline, tenant_id, workflow_id, node_id)
    WHERE activated_at IS NOT NULL AND resolved_at IS NULL;

-- 调用方须持有流程实例行锁。
CREATE FUNCTION scheduler_complete_workflow_node(
    p_tenant text, p_flow text, p_node text, p_expected text, p_success boolean
) RETURNS void LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    UPDATE scheduler_workflow_nodes SET state = CASE WHEN p_success THEN 'succeeded' ELSE 'failed' END
     WHERE tenant_id = p_tenant AND workflow_id = p_flow AND node_id = p_node AND state = p_expected;
    IF NOT FOUND THEN RETURN; END IF;

    IF p_success THEN
        UPDATE scheduler_workflow_nodes n SET state = 'ready'
         WHERE n.tenant_id = p_tenant AND n.workflow_id = p_flow AND n.state = 'blocked'
           AND EXISTS (SELECT 1 FROM scheduler_workflow_edges e
                WHERE e.tenant_id = n.tenant_id AND e.workflow_id = p_flow AND e.predecessor = p_node AND e.successor = n.node_id)
           AND NOT EXISTS (SELECT 1 FROM scheduler_workflow_edges e JOIN scheduler_workflow_nodes p
                ON (p.tenant_id, p.workflow_id, p.node_id) = (e.tenant_id, e.workflow_id, e.predecessor)
                WHERE e.tenant_id = n.tenant_id AND e.workflow_id = p_flow AND e.successor = n.node_id AND p.state <> 'succeeded');
    ELSE
        WITH RECURSIVE descendants(id) AS (
            SELECT e.successor FROM scheduler_workflow_edges e
             WHERE e.tenant_id = p_tenant AND e.workflow_id = p_flow AND e.predecessor = p_node
            UNION
            SELECT e.successor FROM descendants d JOIN scheduler_workflow_edges e ON e.predecessor = d.id
             WHERE e.tenant_id = p_tenant AND e.workflow_id = p_flow
        )
        UPDATE scheduler_workflow_nodes n SET state = 'skipped' FROM descendants d
         WHERE n.tenant_id = p_tenant AND n.workflow_id = p_flow AND n.node_id = d.id AND n.state = 'blocked';
    END IF;

    UPDATE scheduler_workflows w SET state = CASE WHEN EXISTS (
        SELECT 1 FROM scheduler_workflow_nodes n WHERE n.tenant_id = w.tenant_id AND n.workflow_id = w.id AND n.state = 'failed'
    ) THEN 'failed' ELSE 'succeeded' END
    WHERE w.tenant_id = p_tenant AND w.id = p_flow AND NOT EXISTS (
        SELECT 1 FROM scheduler_workflow_nodes n WHERE n.tenant_id = w.tenant_id AND n.workflow_id = w.id
          AND n.state IN ('blocked','ready','queued','waiting')
    );
END $$;

CREATE OR REPLACE FUNCTION scheduler_advance_workflow() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    flow text;
    node text;
BEGIN
    SELECT workflow_id, node_id INTO flow, node FROM scheduler_workflow_nodes
      WHERE run_id = NEW.id AND tenant_id = NEW.tenant_id;
    IF NOT FOUND THEN RETURN NEW; END IF;
    PERFORM 1 FROM scheduler_workflows WHERE tenant_id = NEW.tenant_id AND id = flow FOR UPDATE;
    PERFORM scheduler_complete_workflow_node(NEW.tenant_id, flow, node, 'queued', NEW.status = 'done');
    RETURN NEW;
END $$;
