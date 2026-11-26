ALTER TABLE cmp_cluster_workloads
    ADD COLUMN watch_snapshot_id uuid,
    ADD COLUMN watch_binding bytea,
    ADD COLUMN watch_versions jsonb,
    ADD CONSTRAINT cmp_cluster_workloads_watch_checkpoint CHECK (
        (watch_snapshot_id IS NULL AND watch_binding IS NULL AND watch_versions IS NULL)
        OR (watch_snapshot_id IS NOT NULL AND watch_binding IS NOT NULL AND watch_versions IS NOT NULL
            AND octet_length(watch_binding)=32
            AND jsonb_typeof(watch_versions)='array' AND jsonb_array_length(watch_versions) BETWEEN 1 AND 160)
    );
