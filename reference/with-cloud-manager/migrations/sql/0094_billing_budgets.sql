CREATE TABLE cmp_billing_budgets (
    budget_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    account_id text NOT NULL,
    source text NOT NULL CHECK (source IN ('native','focus-1.0')),
    period date NOT NULL CHECK (extract(day FROM period)=1),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    basis text NOT NULL CHECK (basis IN ('billed','effective')),
    amount numeric(38,12) NOT NULL CHECK (amount > 0 AND amount <> 'NaN'::numeric),
    warning_percent integer NOT NULL CHECK (warning_percent BETWEEN 1 AND 99),
    enabled boolean NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    UNIQUE (tenant_id,project_id,account_id,source,period,currency,basis)
);
CREATE INDEX cmp_billing_budgets_account ON cmp_billing_budgets (tenant_id,account_id,project_id);

ALTER TABLE cmp_project_alerts DROP CONSTRAINT cmp_project_alerts_rule_check;
ALTER TABLE cmp_project_alerts ADD CONSTRAINT cmp_project_alerts_rule_check
    CHECK (rule IN ('operation_review','operation_delayed','scan_failures','scan_stale','billing_budget_warning','billing_budget_exceeded'));

ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation','catalog','alert','console_access','billing_budget'));

CREATE VIEW cmp_billing_budget_status AS
SELECT b.*, latest.revision AS report_revision, spend.amount AS spend,
    CASE
        WHEN NOT b.enabled OR a.project_id<>b.project_id OR a.mode NOT IN ('read_only','write_enabled') THEN 'inactive'
        WHEN spend.amount IS NULL THEN 'unavailable'
        WHEN spend.amount >= b.amount THEN 'exceeded'
        WHEN spend.amount*100 >= b.amount*b.warning_percent THEN 'warning'
        ELSE 'normal'
    END AS state
FROM cmp_billing_budgets AS b
JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
LEFT JOIN LATERAL (
    SELECT r.id,r.revision FROM cmp_billing_reports AS r
    WHERE (r.tenant_id,r.project_id,r.account_id,r.source,r.period)=(b.tenant_id,b.project_id,b.account_id,b.source,b.period)
    ORDER BY r.revision DESC LIMIT 1
) AS latest ON true
LEFT JOIN cmp_billing_totals AS t ON t.report_id=latest.id AND t.currency=b.currency
CROSS JOIN LATERAL (SELECT CASE b.basis WHEN 'billed' THEN t.billed ELSE t.effective END AS amount) AS spend;

CREATE FUNCTION cmp_guard_billing_budget_resolution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    budget_state text;
BEGIN
    SELECT state INTO STRICT budget_state
    FROM cmp_billing_budget_status
    WHERE budget_id=OLD.subject_id::uuid AND (tenant_id,project_id)=(OLD.tenant_id,OLD.project_id);
    IF budget_state='unavailable' OR OLD.rule='billing_budget_' || budget_state THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_billing_budget_resolution_guard BEFORE UPDATE OF resolved_at ON cmp_project_alerts
    FOR EACH ROW WHEN (OLD.resolved_at IS NULL AND NEW.resolved_at IS NOT NULL
        AND OLD.rule IN ('billing_budget_warning','billing_budget_exceeded'))
    EXECUTE FUNCTION cmp_guard_billing_budget_resolution();

CREATE FUNCTION cmp_recheck_billing_budget(target uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    budget cmp_billing_budget_status%ROWTYPE;
    desired_rule text;
BEGIN
    SELECT * INTO STRICT budget FROM cmp_billing_budget_status WHERE budget_id=target;
    IF budget.state='unavailable' THEN
        RETURN;
    END IF;
    desired_rule := CASE budget.state
        WHEN 'warning' THEN 'billing_budget_warning'
        WHEN 'exceeded' THEN 'billing_budget_exceeded'
    END;
    UPDATE cmp_project_alerts AS a
    SET resolved_at=GREATEST(clock_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
    WHERE (a.tenant_id,a.project_id,a.subject_id)=(budget.tenant_id,budget.project_id,target::text)
      AND a.rule IN ('billing_budget_warning','billing_budget_exceeded')
      AND a.rule IS DISTINCT FROM desired_rule AND a.resolved_at IS NULL;
    IF desired_rule IS NOT NULL THEN
        INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status)
        VALUES (budget.tenant_id,budget.project_id,desired_rule,target::text,'billing-budget',budget.account_id,'',budget.state)
        ON CONFLICT (tenant_id,project_id,rule,subject_id) WHERE resolved_at IS NULL DO NOTHING;
    END IF;
END;
$$;

CREATE FUNCTION cmp_recheck_budgets_after_report() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM cmp_recheck_billing_budget(b.budget_id)
    FROM cmp_billing_budgets AS b JOIN (
        SELECT DISTINCT r.tenant_id,r.project_id,r.account_id,r.source,r.period
        FROM (SELECT DISTINCT report_id FROM new_totals) AS t JOIN cmp_billing_reports AS r ON r.id=t.report_id
    ) AS scope USING (tenant_id,project_id,account_id,source,period)
    WHERE b.enabled
    ORDER BY b.budget_id;
    RETURN NULL;
END;
$$;

CREATE TRIGGER cmp_billing_budget_after_report AFTER INSERT ON cmp_billing_totals
    REFERENCING NEW TABLE AS new_totals FOR EACH STATEMENT EXECUTE FUNCTION cmp_recheck_budgets_after_report();

CREATE FUNCTION cmp_recheck_budgets_after_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM cmp_recheck_billing_budget(b.budget_id)
    FROM cmp_billing_budgets AS b
    WHERE (b.tenant_id,b.account_id)=(NEW.tenant_id,NEW.account_id)
      AND b.project_id IN (OLD.project_id,NEW.project_id) AND b.enabled
    ORDER BY b.budget_id;
    RETURN NULL;
END;
$$;

CREATE TRIGGER cmp_billing_budget_after_account AFTER UPDATE OF project_id,mode ON cmp_cloud_accounts
    FOR EACH ROW WHEN (OLD.project_id IS DISTINCT FROM NEW.project_id OR
        (OLD.mode IN ('read_only','write_enabled')) IS DISTINCT FROM (NEW.mode IN ('read_only','write_enabled')))
    EXECUTE FUNCTION cmp_recheck_budgets_after_account();
