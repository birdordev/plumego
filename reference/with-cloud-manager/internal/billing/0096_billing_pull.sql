CREATE TABLE cmp_billing_pull_jobs (
    job_id text PRIMARY KEY CHECK (job_id ~ '^[0-9a-f]{64}$'),
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    account_id text NOT NULL,
    source text NOT NULL CHECK (source IN ('native','focus-1.0')),
    period date NOT NULL CHECK (extract(day FROM period)=1),
    generation bigint NOT NULL CHECK (generation > 0),
    profile_digest text NOT NULL CHECK (profile_digest ~ '^[0-9a-f]{64}$'),
    lease_token uuid,
    lease_until timestamptz,
    next_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    attempted_at timestamptz,
    failures integer NOT NULL DEFAULT 0 CHECK (failures >= 0),
    outcome text NOT NULL DEFAULT 'pending',
    observation text,
    report_revision bigint CHECK (report_revision > 0),
    CHECK ((lease_token IS NULL)=(lease_until IS NULL)),
    CHECK ((observation IS NULL)=(report_revision IS NULL)),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    UNIQUE (tenant_id,project_id,account_id,source,period)
);

CREATE TABLE cmp_billing_pull_batches (
    job_id text NOT NULL REFERENCES cmp_billing_pull_jobs,
    observation text NOT NULL CHECK (observation ~ '^[0-9a-f]{64}$'),
    report_id uuid NOT NULL REFERENCES cmp_billing_reports,
    generation bigint NOT NULL CHECK (generation > 0),
    region text NOT NULL,
    bucket text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (job_id,observation)
);

CREATE TABLE cmp_billing_pull_objects (
    job_id text NOT NULL,
    observation text NOT NULL,
    object_no integer NOT NULL CHECK (object_no BETWEEN 1 AND 32),
    object_key text NOT NULL,
    format text NOT NULL CHECK (format IN ('csv','zip','gzip')),
    etag text NOT NULL,
    entries jsonb,
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    raw bytea NOT NULL CHECK (octet_length(raw) BETWEEN 1 AND 67108864),
    PRIMARY KEY (job_id,observation,object_no),
    FOREIGN KEY (job_id,observation) REFERENCES cmp_billing_pull_batches
);
