WITH scoped AS (
    SELECT s.schedule_id,s.account_id,a.provider,s.region,s.kind,s.next_at,
        CASE WHEN s.lease_until>transaction_timestamp() THEN s.lease_until END AS schedule_lease_until,
        CASE WHEN d.lease_until>transaction_timestamp() THEN d.lease_until END AS account_lease_until,
        CASE WHEN d.cooldown_until>transaction_timestamp() THEN d.cooldown_until END AS account_cooldown_until,
        CASE WHEN i.completed_credential_revision=a.credential_revision THEN i.completed_at END AS completed_at,
        COALESCE(i.credential_revision=a.credential_revision AND i.running AND i.lease_until>transaction_timestamp(),false) AS running
    FROM cmp_inventory_schedules AS s
    JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
    LEFT JOIN cmp_inventory_scopes AS i USING (tenant_id,account_id,region,kind)
    LEFT JOIN cmp_inventory_account_dispatch AS d USING (tenant_id,account_id)
    WHERE a.tenant_id=$1 AND a.mode IN ('read_only','write_enabled') AND s.enabled AND s.target_id=''
        AND (a.project_id=$2 OR EXISTS (
            SELECT 1 FROM cmp_cloud_account_grants AS g
            WHERE (g.tenant_id,g.account_id,g.project_id)=(a.tenant_id,a.account_id,$2)
        ))
        AND ($3='' OR a.provider=$3) AND ($4='' OR a.account_id=$4)
), freshness AS (
    SELECT scoped.*,CASE
        WHEN completed_at IS NULL THEN 'pending'
        WHEN completed_at>=transaction_timestamp()-$5*interval '1 second' THEN 'fresh'
        ELSE 'stale' END AS state,
    CASE
        WHEN schedule_lease_until IS NOT NULL THEN 'leased'
        WHEN next_at>transaction_timestamp() AND (account_cooldown_until IS NULL OR next_at>=account_cooldown_until) THEN 'scheduled'
        WHEN account_cooldown_until IS NOT NULL THEN 'cooldown'
        WHEN account_lease_until IS NOT NULL THEN 'account_busy'
        ELSE 'ready' END AS dispatch_state
    FROM scoped
)
