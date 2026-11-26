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
                    CASE
                        WHEN o.missing_generation IS NOT NULL THEN 'missing_candidate'
                        WHEN a.mode NOT IN ('read_only','write_enabled') THEN 'account_unavailable'
                        WHEN o.tag_observed_at IS NULL THEN 'unobserved'
                        WHEN a.credential_revision<>o.tag_credential_revision
                            OR (parent_scope.scope_id IS NOT NULL AND a.credential_revision<>parent_scope.credential_revision)
                            THEN 'credentials_changed'
                        WHEN o.tag_observed_at<statement_timestamp()-$9*interval '1 second' THEN 'observation_expired'
                        ELSE ''
                    END AS reason
                FROM cmp_inventory_objects AS o
