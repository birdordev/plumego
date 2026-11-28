CREATE TABLE cmp_inventory_history (
    event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_id bigint NOT NULL REFERENCES cmp_inventory_scopes,
    external_id text NOT NULL,
    action text NOT NULL CHECK (action IN ('discovered','changed','missing_candidate','restored','removed')),
    previous_name text,
    previous_status text,
    name text NOT NULL,
    status text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

CREATE INDEX cmp_inventory_history_scope_order
    ON cmp_inventory_history (scope_id, occurred_at DESC, event_id DESC);

CREATE FUNCTION cmp_record_inventory_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    change text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO cmp_inventory_history (scope_id,external_id,action,name,status)
            VALUES (NEW.scope_id,NEW.external_id,'discovered',NEW.name,NEW.status);
        RETURN NEW;
    END IF;

    change := CASE
        WHEN OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL THEN 'removed'
        WHEN (OLD.removed_at IS NOT NULL OR OLD.missing_generation IS NOT NULL)
             AND NEW.removed_at IS NULL AND NEW.missing_generation IS NULL THEN 'restored'
        WHEN OLD.missing_generation IS NULL AND NEW.missing_generation IS NOT NULL THEN 'missing_candidate'
        ELSE 'changed'
    END;
    INSERT INTO cmp_inventory_history (scope_id,external_id,action,previous_name,previous_status,name,status)
        VALUES (NEW.scope_id,NEW.external_id,change,OLD.name,OLD.status,NEW.name,NEW.status);
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_inventory_discovered
    AFTER INSERT ON cmp_inventory_objects
    FOR EACH ROW EXECUTE FUNCTION cmp_record_inventory_history();

CREATE TRIGGER cmp_inventory_changed
    AFTER UPDATE ON cmp_inventory_objects
    FOR EACH ROW WHEN (
        OLD.name IS DISTINCT FROM NEW.name OR OLD.status IS DISTINCT FROM NEW.status
        OR (OLD.missing_generation IS NULL) <> (NEW.missing_generation IS NULL)
        OR (OLD.removed_at IS NULL) <> (NEW.removed_at IS NULL)
    ) EXECUTE FUNCTION cmp_record_inventory_history();
