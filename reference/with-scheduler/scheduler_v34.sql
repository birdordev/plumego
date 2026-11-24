CREATE TABLE scheduler_reservations (
    run_id text PRIMARY KEY REFERENCES scheduled_runs (id),
    tenant_id text NOT NULL,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1000000),
    released_at timestamptz,
    release_key text UNIQUE,
    CHECK ((released_at IS NULL) = (release_key IS NULL))
);

CREATE TABLE scheduler_compensations (
    reservation_id text PRIMARY KEY REFERENCES scheduler_reservations (run_id),
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    node_id text NOT NULL,
    sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    effect_key text NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'registered'
        CHECK (state IN ('registered','queued','manual','released')),
    FOREIGN KEY (tenant_id,workflow_id,node_id)
        REFERENCES scheduler_workflow_nodes (tenant_id,workflow_id,node_id)
);

CREATE INDEX scheduler_compensation_order
    ON scheduler_compensations (tenant_id,workflow_id,sequence DESC) WHERE state <> 'released';
CREATE INDEX scheduler_compensation_page ON scheduler_compensations (tenant_id,workflow_id,reservation_id);

CREATE TABLE scheduler_compensation_runs (
    run_id text PRIMARY KEY REFERENCES scheduled_runs (id),
    reservation_id text NOT NULL REFERENCES scheduler_compensations (reservation_id)
);

CREATE INDEX scheduler_compensation_run_history ON scheduler_compensation_runs (reservation_id,run_id);

CREATE FUNCTION scheduler_mark_compensation_failure() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE
    reservation text;
    flow text;
BEGIN
    SELECT c.reservation_id,c.workflow_id INTO reservation,flow
    FROM scheduler_compensation_runs j JOIN scheduler_compensations c USING (reservation_id)
    WHERE j.run_id=NEW.id AND c.tenant_id=NEW.tenant_id;
    IF NOT FOUND THEN RETURN NEW; END IF;
    PERFORM 1 FROM scheduler_workflows WHERE tenant_id=NEW.tenant_id AND id=flow FOR UPDATE;
    UPDATE scheduler_compensations SET state='manual'
    WHERE reservation_id=reservation AND tenant_id=NEW.tenant_id AND state='queued';
    RETURN NEW;
END $$;

CREATE TRIGGER scheduler_a_compensation_failure AFTER UPDATE OF status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status IN ('queued','leased') AND NEW.status IN ('dead','canceled','expired'))
    EXECUTE FUNCTION scheduler_mark_compensation_failure();

CREATE TABLE scheduler_compensation_commands (
    tenant_id text NOT NULL,
    workflow_id text NOT NULL,
    reservation_id text NOT NULL REFERENCES scheduler_compensations(reservation_id),
    request_key text NOT NULL,
    action text NOT NULL CHECK (action IN ('retry','release')),
    PRIMARY KEY (tenant_id,workflow_id,reservation_id,request_key)
);
