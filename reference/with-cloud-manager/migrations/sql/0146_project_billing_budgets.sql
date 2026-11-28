ALTER TABLE cmp_billing_budgets
    ALTER COLUMN account_id DROP NOT NULL,
    ADD COLUMN project_accounts text[],
    ADD CONSTRAINT cmp_billing_project_budget_scope CHECK (
        (account_id IS NULL) = (project_accounts IS NOT NULL) AND
        (project_accounts IS NULL OR (cardinality(project_accounts) BETWEEN 1 AND 100
            AND array_position(project_accounts,NULL) IS NULL)));

CREATE UNIQUE INDEX cmp_billing_project_budget_key ON cmp_billing_budgets
    (tenant_id,project_id,period,source,currency,basis) WHERE account_id IS NULL;

CREATE UNIQUE INDEX cmp_billing_project_budget_active_alert ON cmp_project_alerts
    (tenant_id,project_id,subject_id) WHERE resource_kind='billing-project-budget' AND resolved_at IS NULL;

CREATE VIEW cmp_billing_project_budget_status AS
SELECT b.*, c.observed_accounts, c.accounts,
    CASE WHEN c.observed_accounts=cardinality(b.project_accounts) THEN c.amount END AS spend,
    CASE
        WHEN NOT b.enabled THEN 'inactive'
        WHEN c.observed_accounts<>cardinality(b.project_accounts) THEN 'unavailable'
        WHEN c.amount>=b.amount THEN 'exceeded'
        WHEN c.amount*100>=b.amount*b.warning_percent THEN 'warning'
        ELSE 'normal'
    END AS state
FROM cmp_billing_budgets AS b
CROSS JOIN LATERAL (
    SELECT count(spend.amount) AS observed_accounts,sum(spend.amount) AS amount,
        jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
            'account',selected.account_id,'report_revision',r.revision::text,'amount',spend.amount::text,
            'state',CASE
                WHEN a.account_id IS NULL THEN 'account_unavailable'
                WHEN r.id IS NULL THEN 'report_missing'
                WHEN spend.amount IS NULL THEN 'amount_missing'
                ELSE 'observed'
            END
        )) ORDER BY selected.account_id) AS accounts
    FROM unnest(b.project_accounts) AS selected(account_id)
    LEFT JOIN cmp_cloud_accounts AS a
      ON (a.tenant_id,a.project_id,a.account_id)=(b.tenant_id,b.project_id,selected.account_id)
         AND a.mode IN ('read_only','write_enabled')
         AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs')
    LEFT JOIN LATERAL (
        SELECT r.id,r.revision FROM cmp_billing_reports AS r
        WHERE (r.tenant_id,r.project_id,r.account_id,r.source,r.period)=(b.tenant_id,b.project_id,a.account_id,b.source,b.period)
            AND r.provider=a.provider
        ORDER BY r.revision DESC LIMIT 1
    ) AS r ON true
    LEFT JOIN cmp_billing_totals AS t ON (t.report_id,t.currency)=(r.id,b.currency)
    CROSS JOIN LATERAL (SELECT CASE b.basis WHEN 'billed' THEN t.billed ELSE t.effective END AS amount) AS spend
) AS c
WHERE b.account_id IS NULL;

CREATE OR REPLACE FUNCTION cmp_guard_billing_budget_resolution() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    budget_state text;
BEGIN
    IF OLD.resource_kind='billing-project-budget' THEN
        SELECT state INTO STRICT budget_state
        FROM cmp_billing_project_budget_status
        WHERE budget_id=OLD.subject_id::uuid AND (tenant_id,project_id)=(OLD.tenant_id,OLD.project_id);
    ELSE
        SELECT state INTO STRICT budget_state
        FROM cmp_billing_budget_status
        WHERE budget_id=OLD.subject_id::uuid AND (tenant_id,project_id)=(OLD.tenant_id,OLD.project_id);
    END IF;
    IF budget_state='unavailable' OR OLD.rule='billing_budget_' || budget_state THEN
        RETURN NULL;
    END IF;
    RETURN NEW;
END;
$$;
