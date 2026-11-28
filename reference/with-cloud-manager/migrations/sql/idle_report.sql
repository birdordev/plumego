SELECT o.external_id,o.name,e.window_start,e.window_end,e.period_seconds,e.cpu_samples,
    e.network_samples,e.cpu_peak,e.network_peak,e.fingerprint,
    CASE
        WHEN o.missing_generation IS NOT NULL OR a.mode NOT IN ('read_only','write_enabled')
             OR a.credential_revision<>s.credential_revision OR o.seen_generation<>s.generation
             OR o.observed_at<=statement_timestamp()-interval '30 hours' THEN 'inventory_stale'
        WHEN lower(o.status) NOT IN ('running','active') THEN 'instance_not_running'
        WHEN e.scope_id IS NULL THEN 'metrics_missing'
        WHEN e.credential_revision<>a.credential_revision OR e.project_id<>(/* PROJECT_OWNER */)
             OR e.ownership_revision<>(/* OWNERSHIP_VERSION */) THEN 'binding_changed'
        WHEN e.window_end<=statement_timestamp()-interval '30 hours' THEN 'metrics_stale'
        WHEN e.cpu_peak>$10 OR e.network_peak>$11 THEN ''
        WHEN e.cpu_samples*e.period_seconds<>604800 OR e.network_samples*e.period_seconds<>604800
             THEN 'incomplete_metrics'
        ELSE ''
    END AS reason
