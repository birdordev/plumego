ALTER TABLE schedules ADD COLUMN source_request_id text NOT NULL DEFAULT '',
    ADD COLUMN source_trace_id text NOT NULL DEFAULT '',
    ADD COLUMN source_span_id text NOT NULL DEFAULT '';
