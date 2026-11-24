CREATE INDEX scheduled_runs_tenant_created_id ON scheduled_runs (tenant_id, created_at DESC, id DESC);
