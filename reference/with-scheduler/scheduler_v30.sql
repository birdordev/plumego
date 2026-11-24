CREATE TABLE scheduler_workflows (
    tenant_id text NOT NULL,
    id text NOT NULL,
    request_key text NOT NULL,
    request_hash bytea NOT NULL,
    state text NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'succeeded', 'failed')),
    dispatch_after timestamptz NOT NULL DEFAULT statement_timestamp(),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, request_key)
);

CREATE INDEX scheduler_workflows_dispatch ON scheduler_workflows (dispatch_after, created_at, tenant_id, id)
    WHERE state = 'running';

CREATE TABLE scheduler_workflow_nodes (
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    node_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('blocked','ready','queued','succeeded','failed','skipped')),
    submission jsonb NOT NULL,
    run_id text UNIQUE REFERENCES scheduled_runs (id),
    PRIMARY KEY (tenant_id, workflow_id, node_id),
    FOREIGN KEY (tenant_id, workflow_id) REFERENCES scheduler_workflows (tenant_id, id)
);

CREATE INDEX scheduler_workflow_ready ON scheduler_workflow_nodes (tenant_id, workflow_id, node_id) WHERE state = 'ready';

CREATE TABLE scheduler_workflow_edges (
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    predecessor text NOT NULL,
    successor text NOT NULL,
    PRIMARY KEY (tenant_id, workflow_id, predecessor, successor),
    FOREIGN KEY (tenant_id, workflow_id, predecessor) REFERENCES scheduler_workflow_nodes (tenant_id, workflow_id, node_id),
    FOREIGN KEY (tenant_id, workflow_id, successor) REFERENCES scheduler_workflow_nodes (tenant_id, workflow_id, node_id),
    CHECK (predecessor <> successor)
);

CREATE INDEX scheduler_workflow_predecessors ON scheduler_workflow_edges (tenant_id, workflow_id, successor, predecessor);

CREATE FUNCTION scheduler_advance_workflow() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    flow text;
    node text;
BEGIN
    SELECT workflow_id, node_id INTO flow, node FROM scheduler_workflow_nodes
      WHERE run_id = NEW.id AND tenant_id = NEW.tenant_id;
    IF NOT FOUND THEN RETURN NEW; END IF;

    -- 同一流程的多个终态事务按流程行串行更新，后继只会就绪一次。
    PERFORM 1 FROM scheduler_workflows WHERE tenant_id = NEW.tenant_id AND id = flow FOR UPDATE;
    UPDATE scheduler_workflow_nodes SET state = CASE WHEN NEW.status = 'done' THEN 'succeeded' ELSE 'failed' END
     WHERE tenant_id = NEW.tenant_id AND workflow_id = flow AND node_id = node AND state = 'queued';
    IF NOT FOUND THEN RETURN NEW; END IF;

    IF NEW.status = 'done' THEN
        UPDATE scheduler_workflow_nodes n SET state = 'ready'
         WHERE n.tenant_id = NEW.tenant_id AND n.workflow_id = flow AND n.state = 'blocked'
           AND EXISTS (SELECT 1 FROM scheduler_workflow_edges e
                WHERE e.tenant_id = n.tenant_id AND e.workflow_id = flow AND e.predecessor = node AND e.successor = n.node_id)
           AND NOT EXISTS (SELECT 1 FROM scheduler_workflow_edges e JOIN scheduler_workflow_nodes p
                ON (p.tenant_id, p.workflow_id, p.node_id) = (e.tenant_id, e.workflow_id, e.predecessor)
                WHERE e.tenant_id = n.tenant_id AND e.workflow_id = flow AND e.successor = n.node_id AND p.state <> 'succeeded');
    ELSE
        WITH RECURSIVE descendants(id) AS (
            SELECT e.successor FROM scheduler_workflow_edges e
             WHERE e.tenant_id = NEW.tenant_id AND e.workflow_id = flow AND e.predecessor = node
            UNION
            SELECT e.successor FROM descendants d JOIN scheduler_workflow_edges e ON e.predecessor = d.id
             WHERE e.tenant_id = NEW.tenant_id AND e.workflow_id = flow
        )
        UPDATE scheduler_workflow_nodes n SET state = 'skipped' FROM descendants d
         WHERE n.tenant_id = NEW.tenant_id AND n.workflow_id = flow AND n.node_id = d.id AND n.state = 'blocked';
    END IF;

    UPDATE scheduler_workflows w SET state = CASE WHEN EXISTS (
        SELECT 1 FROM scheduler_workflow_nodes n WHERE n.tenant_id = w.tenant_id AND n.workflow_id = w.id AND n.state = 'failed'
    ) THEN 'failed' ELSE 'succeeded' END
    WHERE w.tenant_id = NEW.tenant_id AND w.id = flow AND NOT EXISTS (
        SELECT 1 FROM scheduler_workflow_nodes n WHERE n.tenant_id = w.tenant_id AND n.workflow_id = w.id
          AND n.state IN ('blocked','ready','queued')
    );
    RETURN NEW;
END $$;

-- 先锁流程行再由 scheduler_account_task 变更租户配额，与分发事务保持相同顺序。
CREATE TRIGGER scheduler_a_advance_workflow AFTER UPDATE OF status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status IN ('queued','leased') AND NEW.status IN ('done','dead','canceled','expired'))
    EXECUTE FUNCTION scheduler_advance_workflow();
