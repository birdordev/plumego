WITH page AS (
    SELECT s.account_id,a.provider,s.region,s.kind,candidate.*
    FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
    CROSS JOIN LATERAL (
        SELECT classified.* FROM (
            SELECT assessed.*,
                CASE WHEN reason<>'' THEN 'unknown'
                     WHEN tag_keys @> ARRAY(SELECT jsonb_array_elements_text($10::jsonb)) THEN 'compliant'
                     ELSE 'noncompliant' END AS state
            FROM (
                SELECT o.external_id,o.name,o.tag_observed_at,o.tag_keys,
