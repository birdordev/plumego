ALTER TABLE scheduler_shard_members
    ADD COLUMN region text NOT NULL DEFAULT 'local';

ALTER TABLE scheduler_shards
    ADD COLUMN region text NOT NULL DEFAULT 'local',
    ADD COLUMN region_epoch bigint NOT NULL DEFAULT 1 CHECK (region_epoch > 0);

CREATE TABLE scheduler_region_transfers (
    shard integer NOT NULL REFERENCES scheduler_shards (shard),
    previous_epoch bigint NOT NULL CHECK (previous_epoch > 0),
    previous_region text NOT NULL,
    next_region text NOT NULL CHECK (next_region <> previous_region),
    actor text NOT NULL,
    transferred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (shard, previous_epoch)
);
