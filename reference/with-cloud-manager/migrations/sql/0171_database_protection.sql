ALTER TABLE cmp_database_backups ADD COLUMN latest_ready_started_at timestamptz;
ALTER TABLE cmp_database_restore_windows ADD COLUMN latest_window_end_at timestamptz;

CREATE FUNCTION cmp_latest_database_evidence(records jsonb, time_field text, ready_only boolean)
RETURNS timestamptz LANGUAGE sql STABLE AS $$
    SELECT max((entry->>time_field)::timestamptz)
    FROM jsonb_array_elements(records) AS entry
    WHERE NOT ready_only OR entry->>'status'='ready';
$$;

UPDATE cmp_database_backups
SET latest_ready_started_at=cmp_latest_database_evidence(backups,'started_at',true);

UPDATE cmp_database_restore_windows
SET latest_window_end_at=cmp_latest_database_evidence(windows,'end_at',false);

CREATE FUNCTION cmp_refresh_database_backup_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.latest_ready_started_at := cmp_latest_database_evidence(NEW.backups,'started_at',true);
    RETURN NEW;
END;
$$;

CREATE FUNCTION cmp_refresh_database_window_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.latest_window_end_at := cmp_latest_database_evidence(NEW.windows,'end_at',false);
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_database_backup_evidence
BEFORE INSERT OR UPDATE OF backups ON cmp_database_backups
FOR EACH ROW EXECUTE FUNCTION cmp_refresh_database_backup_evidence();

CREATE TRIGGER cmp_database_window_evidence
BEFORE INSERT OR UPDATE OF windows ON cmp_database_restore_windows
FOR EACH ROW EXECUTE FUNCTION cmp_refresh_database_window_evidence();
