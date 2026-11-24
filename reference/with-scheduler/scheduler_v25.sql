CREATE TABLE scheduler_execution_budgets (
    scope text NOT NULL CHECK (scope IN ('tenant', 'type', 'downstream')),
    budget_key text NOT NULL CHECK (budget_key <> ''),
    max_concurrent integer NOT NULL DEFAULT 0 CHECK (max_concurrent >= 0),
    min_start_interval_ms integer NOT NULL DEFAULT 0 CHECK (min_start_interval_ms >= 0),
    next_start_at timestamptz,
    PRIMARY KEY (scope, budget_key)
);

CREATE TABLE scheduler_execution_budget_slots (
    scope text NOT NULL,
    budget_key text NOT NULL,
    task_id text NOT NULL REFERENCES mq_tasks (id) ON DELETE CASCADE,
    claim_no integer NOT NULL,
    PRIMARY KEY (scope, budget_key, task_id, claim_no)
);
CREATE INDEX scheduler_budget_slots_claim ON scheduler_execution_budget_slots (task_id, claim_no);

CREATE FUNCTION scheduler_release_budget_slots() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    DELETE FROM scheduler_execution_budget_slots WHERE task_id = OLD.id AND claim_no = OLD.attempts;
    RETURN NULL;
END $$;

CREATE TRIGGER scheduler_release_budget_slots AFTER UPDATE OF status, attempts ON mq_tasks
    FOR EACH ROW WHEN (OLD.status = 'leased' AND (NEW.status <> 'leased' OR NEW.attempts <> OLD.attempts))
    EXECUTE FUNCTION scheduler_release_budget_slots();

CREATE FUNCTION scheduler_reject_expired_renewal() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.lease_until <= clock_timestamp() THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER scheduler_reject_expired_renewal BEFORE UPDATE OF lease_until ON mq_tasks
    FOR EACH ROW WHEN (OLD.status = 'leased' AND NEW.status = 'leased' AND NEW.attempts = OLD.attempts
        AND NEW.lease_until > OLD.lease_until)
    EXECUTE FUNCTION scheduler_reject_expired_renewal();
