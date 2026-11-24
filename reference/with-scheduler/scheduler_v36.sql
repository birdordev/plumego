CREATE TABLE scheduler_event_sources (
    tenant_id text NOT NULL,
    source text NOT NULL,
    ingest_actor text NOT NULL,
    definition_name text NOT NULL,
    definition_version integer NOT NULL,
    max_lateness_seconds integer NOT NULL CHECK (max_lateness_seconds BETWEEN 1 AND 604800),
    cursor_offset bigint NOT NULL DEFAULT 0 CHECK (cursor_offset >= 0),
    highest_offset bigint NOT NULL DEFAULT 0 CHECK (highest_offset >= cursor_offset),
    PRIMARY KEY (tenant_id,source),
    FOREIGN KEY (tenant_id,definition_name,definition_version)
        REFERENCES scheduler_workflow_definitions (tenant_id,name,version)
);

CREATE INDEX scheduler_event_sources_lagging ON scheduler_event_sources (tenant_id,source)
    WHERE cursor_offset < highest_offset;

CREATE TABLE scheduler_event_identities (
    tenant_id text NOT NULL,
    source text NOT NULL,
    event_id text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    workflow_key text NOT NULL,
    handled_offset bigint,
    PRIMARY KEY (tenant_id,source,event_id),
    UNIQUE (tenant_id,workflow_key),
    FOREIGN KEY (tenant_id,source) REFERENCES scheduler_event_sources (tenant_id,source)
);

CREATE TABLE scheduler_source_events (
    tenant_id text NOT NULL,
    source text NOT NULL,
    source_offset bigint NOT NULL CHECK (source_offset > 0),
    event_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    payload jsonb NOT NULL,
    outcome text NOT NULL DEFAULT 'pending' CHECK (outcome IN ('pending','triggered','late','duplicate')),
    workflow_id text,
    PRIMARY KEY (tenant_id,source,source_offset),
    FOREIGN KEY (tenant_id,source,event_id) REFERENCES scheduler_event_identities (tenant_id,source,event_id),
    FOREIGN KEY (tenant_id,workflow_id) REFERENCES scheduler_workflows (tenant_id,id),
    CHECK ((outcome='triggered') = (workflow_id IS NOT NULL))
);
CREATE INDEX scheduler_source_events_pending ON scheduler_source_events (tenant_id,source,source_offset)
    WHERE outcome='pending';
CREATE UNIQUE INDEX scheduler_source_events_workflow ON scheduler_source_events (tenant_id,workflow_id)
    WHERE workflow_id IS NOT NULL;

CREATE TABLE scheduler_event_replays (
    tenant_id text NOT NULL,
    source text NOT NULL,
    id text NOT NULL,
    request_key text NOT NULL,
    first_offset bigint NOT NULL,
    last_offset bigint NOT NULL,
    next_offset bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (tenant_id,source,id),
    UNIQUE (tenant_id,source,request_key),
    FOREIGN KEY (tenant_id,source) REFERENCES scheduler_event_sources (tenant_id,source),
    CHECK (first_offset > 0 AND last_offset >= first_offset AND last_offset-first_offset < 1000),
    CHECK (next_offset >= first_offset AND next_offset <= last_offset+1)
);
CREATE INDEX scheduler_event_replays_pending ON scheduler_event_replays (created_at,tenant_id,source,id)
    WHERE next_offset<=last_offset;
