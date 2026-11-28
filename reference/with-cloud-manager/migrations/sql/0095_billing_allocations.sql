CREATE TABLE cmp_billing_allocations (
    allocation_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL REFERENCES cmp_billing_reports,
    revision bigint NOT NULL CHECK (revision > 0),
    algorithm text NOT NULL CHECK (algorithm <> ''),
    policy_digest text NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    policy jsonb NOT NULL,
    invalid_tag_rows integer NOT NULL CHECK (invalid_tag_rows >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (report_id,revision),
    UNIQUE (report_id,policy_digest)
);

CREATE TABLE cmp_billing_allocation_totals (
    allocation_id uuid NOT NULL REFERENCES cmp_billing_allocations,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    center text NOT NULL CHECK (center ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    billed numeric NOT NULL,
    effective numeric,
    list_cost numeric,
    row_count integer NOT NULL CHECK (row_count >= 0),
    PRIMARY KEY (allocation_id,currency,center)
);
