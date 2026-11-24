CREATE FUNCTION scheduler_jitter_milliseconds(tenant text, schedule text, window_ms integer) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    RETURN (('x' || substr(md5('jitter:' || length(tenant)::text || ':' || tenant || schedule), 1, 8))::bit(32)::bigint
        % (1 + window_ms - least(400, window_ms / 4)))::integer;

ALTER TABLE schedules ADD COLUMN jitter_window_ms integer NOT NULL DEFAULT 0
    CHECK (jitter_window_ms BETWEEN 0 AND 60000);
ALTER TABLE schedules ADD COLUMN next_dispatch_at timestamptz GENERATED ALWAYS AS (
    timezone('UTC', timezone('UTC', next_fire_at) +
        scheduler_jitter_milliseconds(tenant_id, id, jitter_window_ms) * interval '1 millisecond')
) STORED;

DROP INDEX schedules_due;
DROP INDEX schedules_shard_due;
CREATE INDEX schedules_due ON schedules (next_dispatch_at, tenant_id, id) WHERE NOT paused;
CREATE INDEX schedules_shard_due ON schedules (schedule_shard, next_dispatch_at, tenant_id, id) WHERE NOT paused;
