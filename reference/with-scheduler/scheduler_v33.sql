CREATE TABLE scheduler_checkpoint_jobs (
    run_id text PRIMARY KEY REFERENCES scheduled_runs (id),
    shards integer NOT NULL CHECK (shards BETWEEN 1 AND 32),
    items_per_shard integer NOT NULL CHECK (items_per_shard BETWEEN 1 AND 10000),
    segment_size integer NOT NULL CHECK (segment_size BETWEEN 1 AND 256),
    output_version integer NOT NULL CHECK (output_version IN (1,2))
);

CREATE TABLE scheduler_checkpoints (
    run_id text NOT NULL REFERENCES scheduler_checkpoint_jobs (run_id),
    shard integer NOT NULL CHECK (shard BETWEEN 0 AND 31),
    next_offset integer NOT NULL DEFAULT 0 CHECK (next_offset BETWEEN 0 AND 10000),
    generation integer NOT NULL DEFAULT 0 CHECK (generation >= 0),
    PRIMARY KEY (run_id,shard)
);

CREATE TABLE scheduler_checkpoint_segments (
    run_id text NOT NULL,
    shard integer NOT NULL,
    start_offset integer NOT NULL CHECK (start_offset >= 0),
    end_offset integer NOT NULL CHECK (end_offset > start_offset AND end_offset <= 10000),
    output_version integer NOT NULL CHECK (output_version IN (1,2)),
    generation integer NOT NULL CHECK (generation > 0),
    digest bytea NOT NULL CHECK (octet_length(digest) = 32),
    PRIMARY KEY (run_id,shard,start_offset),
    FOREIGN KEY (run_id,shard) REFERENCES scheduler_checkpoints (run_id,shard)
);
