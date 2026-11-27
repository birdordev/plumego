SET LOCAL lock_timeout = '2s';

ALTER TABLE cmp_inventory_objects ADD COLUMN volume_details jsonb;
ALTER TABLE cmp_inventory_objects ADD CONSTRAINT cmp_inventory_volume_details_shape
    CHECK (volume_details IS NULL OR jsonb_typeof(volume_details)='object') NOT VALID;

ALTER TABLE cmp_inventory_history
    ADD COLUMN previous_volume_details jsonb,
    ADD COLUMN volume_details jsonb;

CREATE OR REPLACE FUNCTION cmp_record_inventory_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    change text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO cmp_inventory_history (scope_id,external_id,action,name,status,public_addresses,snapshot,load_balancer,database_instance,object_bucket,cache_instance,kubernetes_cluster,volume_details)
            VALUES (NEW.scope_id,NEW.external_id,'discovered',NEW.name,NEW.status,NEW.public_addresses,NEW.snapshot,NEW.load_balancer,NEW.database_instance,NEW.object_bucket,NEW.cache_instance,NEW.kubernetes_cluster,NEW.volume_details);
        RETURN NEW;
    END IF;

    change := CASE
        WHEN OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL THEN 'removed'
        WHEN (OLD.removed_at IS NOT NULL OR OLD.missing_generation IS NOT NULL)
             AND NEW.removed_at IS NULL AND NEW.missing_generation IS NULL THEN 'restored'
        WHEN OLD.missing_generation IS NULL AND NEW.missing_generation IS NOT NULL THEN 'missing_candidate'
        ELSE 'changed'
    END;
    INSERT INTO cmp_inventory_history (
        scope_id,external_id,action,previous_name,previous_status,name,status,previous_public_addresses,public_addresses,previous_snapshot,snapshot,previous_load_balancer,load_balancer,previous_database_instance,database_instance,previous_object_bucket,object_bucket,previous_cache_instance,cache_instance,previous_kubernetes_cluster,kubernetes_cluster,previous_volume_details,volume_details)
        VALUES (NEW.scope_id,NEW.external_id,change,OLD.name,OLD.status,NEW.name,NEW.status,OLD.public_addresses,NEW.public_addresses,
                OLD.snapshot,NEW.snapshot,OLD.load_balancer,NEW.load_balancer,OLD.database_instance,NEW.database_instance,OLD.object_bucket,NEW.object_bucket,OLD.cache_instance,NEW.cache_instance,OLD.kubernetes_cluster,NEW.kubernetes_cluster,OLD.volume_details,NEW.volume_details);
    RETURN NEW;
END;
$$;

DROP TRIGGER cmp_inventory_changed ON cmp_inventory_objects;
CREATE TRIGGER cmp_inventory_changed
    AFTER UPDATE ON cmp_inventory_objects
    FOR EACH ROW WHEN (
        OLD.name IS DISTINCT FROM NEW.name OR OLD.status IS DISTINCT FROM NEW.status
        OR OLD.public_addresses IS DISTINCT FROM NEW.public_addresses OR OLD.snapshot IS DISTINCT FROM NEW.snapshot
        OR OLD.load_balancer IS DISTINCT FROM NEW.load_balancer
        OR OLD.database_instance IS DISTINCT FROM NEW.database_instance
        OR OLD.object_bucket IS DISTINCT FROM NEW.object_bucket
        OR OLD.cache_instance IS DISTINCT FROM NEW.cache_instance
        OR OLD.kubernetes_cluster IS DISTINCT FROM NEW.kubernetes_cluster
        OR OLD.volume_details IS DISTINCT FROM NEW.volume_details
        OR (OLD.missing_generation IS NULL) <> (NEW.missing_generation IS NULL)
        OR (OLD.removed_at IS NULL) <> (NEW.removed_at IS NULL)
    ) EXECUTE FUNCTION cmp_record_inventory_history();
