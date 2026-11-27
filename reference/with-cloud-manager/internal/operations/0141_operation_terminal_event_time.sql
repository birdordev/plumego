CREATE INDEX cmp_operation_terminal_event_time
    ON cmp_instance_operation_events (recorded_at,event_id)
    WHERE status IN ('succeeded','failed');
