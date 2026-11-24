ALTER TABLE scheduled_runs
    DROP CONSTRAINT scheduled_runs_tenant_id_schedule_id_scheduled_at_key;

CREATE UNIQUE INDEX scheduled_runs_planned_occurrence
    ON scheduled_runs (tenant_id, schedule_id, scheduled_at)
    WHERE request_key IS NULL;
