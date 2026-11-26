ALTER TABLE cmp_inventory_objects
    ADD COLUMN public_addresses jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(public_addresses)='array' AND jsonb_array_length(public_addresses)<=2);

ALTER TABLE cmp_inventory_history
    ADD COLUMN previous_public_addresses jsonb,
    ADD COLUMN public_addresses jsonb NOT NULL DEFAULT '[]'::jsonb;

CREATE OR REPLACE FUNCTION cmp_record_inventory_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    change text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO cmp_inventory_history (scope_id,external_id,action,name,status,public_addresses)
            VALUES (NEW.scope_id,NEW.external_id,'discovered',NEW.name,NEW.status,NEW.public_addresses);
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
        scope_id,external_id,action,previous_name,previous_status,name,status,previous_public_addresses,public_addresses)
        VALUES (NEW.scope_id,NEW.external_id,change,OLD.name,OLD.status,NEW.name,NEW.status,OLD.public_addresses,NEW.public_addresses);
    RETURN NEW;
END;
$$;

DROP TRIGGER cmp_inventory_changed ON cmp_inventory_objects;
CREATE TRIGGER cmp_inventory_changed
    AFTER UPDATE ON cmp_inventory_objects
    FOR EACH ROW WHEN (
        OLD.name IS DISTINCT FROM NEW.name OR OLD.status IS DISTINCT FROM NEW.status
        OR OLD.public_addresses IS DISTINCT FROM NEW.public_addresses
        OR (OLD.missing_generation IS NULL) <> (NEW.missing_generation IS NULL)
        OR (OLD.removed_at IS NULL) <> (NEW.removed_at IS NULL)
    ) EXECUTE FUNCTION cmp_record_inventory_history();
