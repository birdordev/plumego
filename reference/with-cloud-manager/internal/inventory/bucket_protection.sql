, posture AS MATERIALIZED (
    SELECT *,
        CASE WHEN NOT inventory_available OR encryption_time IS NULL THEN 'unobserved'
             WHEN encryption_time < transaction_timestamp() - make_interval(secs => $6::integer) THEN 'stale'
             ELSE 'fresh' END AS encryption_status,
        CASE WHEN NOT inventory_available OR versioning_time IS NULL THEN 'unobserved'
             WHEN versioning_time < transaction_timestamp() - make_interval(secs => $6::integer) THEN 'stale'
             ELSE 'fresh' END AS versioning_status
    FROM visible
), page AS (
    SELECT id,name,account,provider,location,inventory_available,
        jsonb_strip_nulls(jsonb_build_object('status', encryption_status,
            'state', CASE WHEN inventory_available THEN encryption_state END,
            'observed_at', CASE WHEN inventory_available THEN encryption_time END)) AS encryption,
        jsonb_strip_nulls(jsonb_build_object('status', versioning_status,
            'state', CASE WHEN inventory_available THEN versioning_state END,
            'observed_at', CASE WHEN inventory_available THEN versioning_time END)) AS versioning
    FROM posture
    WHERE (account,id) > ($8,$9) AND CASE $7
        WHEN 'inventory_unavailable' THEN NOT inventory_available
        WHEN 'encryption_unconfigured' THEN encryption_status='fresh' AND encryption_state='unconfigured'
        WHEN 'versioning_inactive' THEN versioning_status='fresh' AND versioning_state IN ('disabled','suspended')
        WHEN 'encryption_needs_observation' THEN encryption_status<>'fresh'
        WHEN 'versioning_needs_observation' THEN versioning_status<>'fresh'
        ELSE true END
    ORDER BY account,id LIMIT $10
)
SELECT transaction_timestamp(), count(*), count(*) FILTER (WHERE NOT inventory_available),
    count(*) FILTER (WHERE encryption_status='fresh'),
    count(*) FILTER (WHERE encryption_status='stale'),
    count(*) FILTER (WHERE encryption_status='unobserved'),
    count(*) FILTER (WHERE encryption_status='fresh' AND encryption_state='unconfigured'),
    count(*) FILTER (WHERE versioning_status='fresh'),
    count(*) FILTER (WHERE versioning_status='stale'),
    count(*) FILTER (WHERE versioning_status='unobserved'),
    count(*) FILTER (WHERE versioning_status='fresh' AND versioning_state IN ('disabled','suspended')),
    (SELECT COALESCE(jsonb_agg(to_jsonb(page) ORDER BY account,id),'[]'::jsonb) FROM page)
FROM posture
