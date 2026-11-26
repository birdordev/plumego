CREATE TABLE cmp_billing_reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    account_id text NOT NULL,
    source text NOT NULL CHECK (source IN ('native','focus-1.0')),
    period date NOT NULL CHECK (extract(day FROM period)=1),
    revision bigint NOT NULL CHECK (revision > 0),
    provider text NOT NULL CHECK (provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs')),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    manifest jsonb NOT NULL,
    row_count integer NOT NULL CHECK (row_count >= 0),
    imported_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    UNIQUE (tenant_id,project_id,account_id,source,period,revision),
    UNIQUE (tenant_id,project_id,account_id,source,period,digest)
);

CREATE TABLE cmp_billing_parts (
    report_id uuid NOT NULL REFERENCES cmp_billing_reports,
    part_no integer NOT NULL CHECK (part_no BETWEEN 1 AND 32),
    digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    raw bytea NOT NULL CHECK (octet_length(raw) BETWEEN 1 AND 16777216),
    row_count integer NOT NULL CHECK (row_count >= 0),
    PRIMARY KEY (report_id,part_no),
    UNIQUE (report_id,digest)
);

CREATE TABLE cmp_billing_lines (
    report_id uuid NOT NULL,
    part_no integer NOT NULL,
    row_no integer NOT NULL CHECK (row_no > 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    billed numeric(38,12) NOT NULL,
    effective numeric(38,12),
    list_cost numeric(38,12),
    resource_id text,
    service text,
    region text,
    category text,
    tags text,
    PRIMARY KEY (report_id,part_no,row_no),
    FOREIGN KEY (report_id,part_no) REFERENCES cmp_billing_parts
);

CREATE TABLE cmp_billing_totals (
    report_id uuid NOT NULL REFERENCES cmp_billing_reports,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    billed numeric NOT NULL,
    effective numeric,
    list_cost numeric,
    expected_billed numeric,
    PRIMARY KEY (report_id,currency)
);
