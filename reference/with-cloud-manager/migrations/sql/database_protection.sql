, capabilities AS (
    SELECT *, id ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$' AND CASE provider
        WHEN 'tencent-cvm' THEN length(id)>length(split_part(id,'-',1))+1 AND (
            (product='tencent-cdb' AND engine='mysql' AND split_part(id,'-',1) IN ('cdb','cdbro','cdbdr','cdbaro')) OR
            (product='tencent-postgresql' AND engine='postgresql' AND split_part(id,'-',1)='postgres') OR
            (product='tencent-sqlserver' AND engine='sqlserver' AND split_part(id,'-',1) IN ('mssql','mssqlro','mssqldr'))
        )
        WHEN 'alibaba-ecs' THEN product='alibaba-rds' AND engine IN ('mysql','postgresql','sqlserver','mariadb')
        WHEN 'huawei-ecs' THEN product='huawei-rds' AND engine IN ('mysql','postgresql','sqlserver')
        ELSE false END AS backup_supported
    FROM visible
), posture AS MATERIALIZED (
    SELECT *,
        CASE WHEN NOT backup_supported THEN 'unsupported'
             WHEN NOT inventory_available OR backup_observed_at IS NULL THEN 'unobserved'
             WHEN backup_observed_at < transaction_timestamp()-make_interval(secs => $6::integer) THEN 'stale'
             WHEN latest_ready_started_at IS NULL OR latest_ready_started_at>transaction_timestamp() THEN 'missing'
             WHEN latest_ready_started_at < transaction_timestamp()-make_interval(secs => $7::integer) THEN 'outdated'
             ELSE 'ready' END AS backup_status,
        CASE WHEN NOT backup_supported OR (provider='alibaba-ecs' AND engine='sqlserver') THEN 'unsupported'
             WHEN NOT inventory_available OR restore_observed_at IS NULL THEN 'unobserved'
             WHEN restore_observed_at < transaction_timestamp()-make_interval(secs => $6::integer) THEN 'stale'
             WHEN latest_window_end_at IS NULL OR latest_window_end_at>transaction_timestamp() THEN 'missing'
             WHEN latest_window_end_at < transaction_timestamp()-make_interval(secs => $7::integer) THEN 'outdated'
             ELSE 'ready' END AS restore_status,
        inventory_available AND COALESCE((backup_schedule AND backup_outcome IN ('failure','throttled')) OR
            (restore_schedule AND restore_outcome IN ('failure','throttled')),false) AS refresh_failed
    FROM capabilities
), page AS (
    SELECT id,name,account,region,provider,product,engine,inventory_available,
        jsonb_strip_nulls(jsonb_build_object('status',backup_status,
            'observed_at', CASE WHEN inventory_available THEN backup_observed_at END,
            'latest_evidence_at', CASE WHEN inventory_available THEN latest_ready_started_at END,
            'schedule_enabled', CASE WHEN inventory_available THEN backup_schedule END,
            'last_refresh_outcome', CASE WHEN inventory_available THEN backup_outcome END,
            'last_refresh_at', CASE WHEN inventory_available THEN backup_finished_at END)) AS backups,
        jsonb_strip_nulls(jsonb_build_object('status',restore_status,
            'observed_at', CASE WHEN inventory_available THEN restore_observed_at END,
            'latest_evidence_at', CASE WHEN inventory_available THEN latest_window_end_at END,
            'scope', CASE WHEN inventory_available THEN recovery_scope END,
            'schedule_enabled', CASE WHEN inventory_available THEN restore_schedule END,
            'last_refresh_outcome', CASE WHEN inventory_available THEN restore_outcome END,
            'last_refresh_at', CASE WHEN inventory_available THEN restore_finished_at END)) AS restore_windows
    FROM posture
    WHERE (account,region,id)>($9,$10,$11) AND CASE $8
        WHEN 'inventory_unavailable' THEN NOT inventory_available
        WHEN 'backup_attention' THEN backup_status NOT IN ('ready','unsupported')
        WHEN 'recovery_attention' THEN restore_status NOT IN ('ready','unsupported')
        WHEN 'refresh_failed' THEN refresh_failed
        ELSE true END
    ORDER BY account,region,id LIMIT $12
)
SELECT transaction_timestamp(), jsonb_build_object(
    'total',count(*),'inventory_unavailable',count(*) FILTER (WHERE NOT inventory_available),
    'refresh_failed',count(*) FILTER (WHERE refresh_failed),
    'backups',jsonb_build_object(
        'ready',count(*) FILTER (WHERE backup_status='ready'),
        'outdated',count(*) FILTER (WHERE backup_status='outdated'),
        'missing',count(*) FILTER (WHERE backup_status='missing'),
        'stale',count(*) FILTER (WHERE backup_status='stale'),
        'unobserved',count(*) FILTER (WHERE backup_status='unobserved'),
        'unsupported',count(*) FILTER (WHERE backup_status='unsupported')),
    'restore_windows',jsonb_build_object(
        'ready',count(*) FILTER (WHERE restore_status='ready'),
        'outdated',count(*) FILTER (WHERE restore_status='outdated'),
        'missing',count(*) FILTER (WHERE restore_status='missing'),
        'stale',count(*) FILTER (WHERE restore_status='stale'),
        'unobserved',count(*) FILTER (WHERE restore_status='unobserved'),
        'unsupported',count(*) FILTER (WHERE restore_status='unsupported'))),
    (SELECT COALESCE(jsonb_agg(to_jsonb(page) ORDER BY account,region,id),'[]'::jsonb) FROM page)
FROM posture;
