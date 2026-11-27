WITH months AS (
    SELECT period::date AS period
    FROM generate_series($3::timestamp,$4::timestamp,interval '1 month') AS series(period)
), accounts AS (
    SELECT account_id,provider
    FROM cmp_cloud_accounts
    WHERE (tenant_id,project_id)=($1,$2) AND mode IN ('read_only','write_enabled')
        AND provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs') AND ($7='' OR provider=$7)
), latest AS MATERIALIZED (
    SELECT m.period,a.account_id,a.provider,r.revision,r.digest,r.row_count,t.billed,t.effective,t.list_cost,t.expected_billed
    FROM months AS m CROSS JOIN accounts AS a
    LEFT JOIN LATERAL (
        SELECT id,revision,digest,row_count
        FROM cmp_billing_reports AS r
        WHERE (r.tenant_id,r.project_id,r.account_id,r.source,r.period)=($1,$2,a.account_id,$5,m.period)
          AND r.provider=a.provider
        ORDER BY r.revision DESC LIMIT 1
    ) AS r ON true
    LEFT JOIN cmp_billing_totals AS t ON (t.report_id,t.currency)=(r.id,$6)
), month_totals AS (
    SELECT m.period,count(a.account_id) AS eligible_accounts,count(a.revision) AS published_accounts,count(a.billed) AS currency_accounts,
        sum(a.billed) AS billed,
        CASE WHEN count(a.effective)=count(a.billed) THEN sum(a.effective) END AS effective,
        CASE WHEN count(a.list_cost)=count(a.billed) THEN sum(a.list_cost) END AS list_cost,
        CASE WHEN count(a.expected_billed)=count(a.billed) THEN sum(a.expected_billed) END AS expected_billed
    FROM months AS m LEFT JOIN latest AS a USING (period)
    GROUP BY m.period
), account_page AS (
    SELECT *
    FROM latest
    WHERE ($8='' OR (period,account_id)>(NULLIF($8,'')::date,$9)) AND (NOT $10::bool OR revision IS NULL)
    ORDER BY period,account_id LIMIT $11
)
SELECT transaction_timestamp(),
    (SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
        'period',to_char(period,'YYYY-MM'),'eligible_accounts',eligible_accounts,
        'published_accounts',published_accounts,'currency_accounts',currency_accounts,
        'total',CASE WHEN currency_accounts>0 THEN jsonb_build_object(
            'currency',$6,'billed_cost',billed::text,'effective_cost',effective::text,'list_cost',list_cost::text,
            'expected_billed_cost',expected_billed::text,'variance',(billed-expected_billed)::text
        ) END
    )) ORDER BY period) FROM month_totals),
    COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
        'period',to_char(period,'YYYY-MM'),'account',account_id,'provider',provider,'billed_cost',billed::text,
        'report',CASE WHEN revision IS NOT NULL THEN jsonb_build_object('revision',revision::text,'sha256',digest,'rows',row_count) END
    )) ORDER BY period,account_id) FROM account_page),'[]'::jsonb)
