CREATE TABLE cmp_instance_idle_evidence (
    scope_id bigint NOT NULL,
    external_id text NOT NULL,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    ownership_revision bigint NOT NULL CHECK (ownership_revision>0),
    credential_revision bigint NOT NULL CHECK (credential_revision>0),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL CHECK (window_end-window_start=interval '168 hours'),
    period_seconds integer NOT NULL CHECK (period_seconds IN (600,3600)),
    cpu_samples integer NOT NULL CHECK (cpu_samples>=0 AND cpu_samples*period_seconds<=604800),
    network_samples integer NOT NULL CHECK (network_samples>=0 AND network_samples*period_seconds<=604800),
    cpu_peak double precision CHECK (cpu_peak BETWEEN 0 AND 100),
    network_peak double precision CHECK (network_peak BETWEEN 0 AND 1e12),
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    CHECK ((cpu_samples=0)=(cpu_peak IS NULL) AND (network_samples=0)=(network_peak IS NULL)),
    PRIMARY KEY (scope_id,external_id),
    FOREIGN KEY (scope_id,external_id) REFERENCES cmp_inventory_objects ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects
);

CREATE FUNCTION cmp_invalidate_idle_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM cmp_inventory_scopes WHERE scope_id=NEW.scope_id AND kind='instance') THEN
        DELETE FROM cmp_instance_idle_evidence WHERE (scope_id,external_id)=(NEW.scope_id,NEW.external_id);
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_instance_idle_lifecycle
    AFTER UPDATE OF status,removed_at,missing_generation ON cmp_inventory_objects
    FOR EACH ROW WHEN (
        lower(OLD.status) IS DISTINCT FROM lower(NEW.status) OR OLD.removed_at IS DISTINCT FROM NEW.removed_at
        OR (OLD.missing_generation IS NULL)<>(NEW.missing_generation IS NULL)
    ) EXECUTE FUNCTION cmp_invalidate_idle_evidence();
