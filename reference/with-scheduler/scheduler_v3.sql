ALTER TABLE schedules
    ADD COLUMN misfire_policy text NOT NULL DEFAULT 'catch_up'
        CHECK (misfire_policy IN ('skip', 'coalesce', 'catch_up')),
    ADD COLUMN catch_up_window_seconds integer NOT NULL DEFAULT 3600
        CHECK (catch_up_window_seconds BETWEEN 1 AND 86400),
    ADD COLUMN last_misfire_action text
        CHECK (last_misfire_action IN ('skip', 'coalesce', 'trim')),
    ADD COLUMN last_misfire_at timestamptz;
