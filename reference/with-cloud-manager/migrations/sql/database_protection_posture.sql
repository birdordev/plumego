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
), posture AS (
    SELECT *,
        CASE WHEN NOT backup_supported THEN 'unsupported'
             WHEN NOT inventory_available OR backup_observed_at IS NULL THEN 'unobserved'
             WHEN backup_observed_at < observed_after THEN 'stale'
             WHEN latest_ready_started_at IS NULL OR latest_ready_started_at>transaction_timestamp() THEN 'missing'
             WHEN latest_ready_started_at < evidence_after THEN 'outdated'
             ELSE 'ready' END AS backup_status,
        CASE WHEN NOT backup_supported OR (provider='alibaba-ecs' AND engine='sqlserver') THEN 'unsupported'
             WHEN NOT inventory_available OR restore_observed_at IS NULL THEN 'unobserved'
             WHEN restore_observed_at < observed_after THEN 'stale'
             WHEN latest_window_end_at IS NULL OR latest_window_end_at>transaction_timestamp() THEN 'missing'
             WHEN latest_window_end_at < evidence_after THEN 'outdated'
             ELSE 'ready' END AS restore_status
    FROM capabilities CROSS JOIN limits
)
