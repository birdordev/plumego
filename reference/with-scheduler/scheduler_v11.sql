ALTER TABLE scheduled_runs
    ADD COLUMN advances_fixed_delay boolean NOT NULL DEFAULT false,
    ADD COLUMN delay_completed_at timestamptz,
    ADD COLUMN delay_applied boolean NOT NULL DEFAULT false;

CREATE INDEX scheduled_runs_delay_ready ON scheduled_runs (delay_completed_at, id)
    WHERE delay_completed_at IS NOT NULL AND NOT delay_applied;

ALTER TABLE schedules
    ADD COLUMN fixed_delay_us bigint CHECK (fixed_delay_us BETWEEN 1 AND 31536000000000),
    ADD COLUMN waiting_run_id text REFERENCES scheduled_runs (id),
    ALTER COLUMN next_fire_at DROP NOT NULL,
    ADD CONSTRAINT scheduler_cadence_state CHECK (
        (next_fire_at IS NOT NULL AND waiting_run_id IS NULL)
        OR (next_fire_at IS NULL AND waiting_run_id IS NOT NULL AND fixed_delay_us IS NOT NULL)
    );

CREATE FUNCTION scheduler_record_delay_completion() RETURNS trigger
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
    UPDATE scheduled_runs SET delay_completed_at = clock_timestamp()
    WHERE id = NEW.id AND tenant_id = NEW.tenant_id
      AND advances_fixed_delay AND delay_completed_at IS NULL;
    RETURN NEW;
END
$$;

CREATE TRIGGER scheduler_record_delay_completion AFTER UPDATE OF status ON mq_tasks
    FOR EACH ROW WHEN (OLD.status IN ('queued', 'leased') AND NEW.status IN ('done', 'dead', 'canceled', 'expired'))
    EXECUTE FUNCTION scheduler_record_delay_completion();
