CREATE FUNCTION scheduler_plan_shard(tenant text, schedule text) RETURNS integer
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    RETURN get_byte(decode(md5(length(tenant)::text || ':' || tenant || schedule), 'hex'), 0) % 64;

ALTER TABLE schedules ADD COLUMN schedule_shard integer
    GENERATED ALWAYS AS (scheduler_plan_shard(tenant_id, id)) STORED;
CREATE INDEX schedules_shard_due ON schedules (schedule_shard, next_fire_at, tenant_id, id) WHERE NOT paused;

ALTER TABLE scheduled_runs ADD COLUMN schedule_shard integer
    GENERATED ALWAYS AS (scheduler_plan_shard(tenant_id, schedule_id)) STORED;
CREATE INDEX scheduled_runs_shard_delay_ready ON scheduled_runs (schedule_shard, delay_completed_at, id)
    WHERE delay_completed_at IS NOT NULL AND NOT delay_applied;

CREATE TABLE scheduler_shard_members (
    id text PRIMARY KEY,
    lease_until timestamptz NOT NULL
);

CREATE TABLE scheduler_shards (
    shard integer PRIMARY KEY CHECK (shard BETWEEN 0 AND 63),
    owner_id text
);

INSERT INTO scheduler_shards (shard) SELECT generate_series(0, 63);

CREATE TABLE scheduler_shard_control (
    singleton boolean PRIMARY KEY CHECK (singleton),
    live_members text NOT NULL DEFAULT '',
    transfers_pending boolean NOT NULL DEFAULT true
);
INSERT INTO scheduler_shard_control (singleton) VALUES (true);
