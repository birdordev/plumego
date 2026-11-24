ALTER TABLE scheduled_outbox
    ADD COLUMN available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN lease_token text,
    ADD COLUMN lease_until timestamptz;

CREATE INDEX scheduled_outbox_ready
    ON scheduled_outbox (available_at, created_at, run_id)
    WHERE delivered_at IS NULL;
