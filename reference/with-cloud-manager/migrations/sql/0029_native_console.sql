CREATE TABLE cmp_instance_console_grants (
    grant_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    requester_id text NOT NULL,
    account_id text NOT NULL,
    region text NOT NULL,
    scope_id bigint NOT NULL,
    asset_id text NOT NULL,
    purpose text NOT NULL CHECK (purpose IN ('troubleshoot','maintenance','incident')),
    idempotency_key text NOT NULL,
    asset_revision bigint NOT NULL CHECK (asset_revision>0),
    credential_revision bigint NOT NULL CHECK (credential_revision>0),
    requester_revision bigint NOT NULL CHECK (requester_revision>0),
    reviewer_id text,
    reviewer_revision bigint,
    status text NOT NULL DEFAULT 'awaiting_approval' CHECK (status IN ('awaiting_approval','approved','rejected','revoked','in_use','closed','failed')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision>0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    expires_at timestamptz NOT NULL DEFAULT (statement_timestamp()+interval '15 minutes'),
    actor_id text,
    UNIQUE (tenant_id,project_id,requester_id,idempotency_key),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,requester_id) REFERENCES cmp_principals,
    FOREIGN KEY (tenant_id,reviewer_id) REFERENCES cmp_principals,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES cmp_principals,
    FOREIGN KEY (tenant_id,account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id,scope_id) REFERENCES cmp_inventory_scopes(tenant_id,scope_id),
    FOREIGN KEY (scope_id,asset_id) REFERENCES cmp_inventory_objects,
    CHECK ((reviewer_id IS NULL)=(reviewer_revision IS NULL)),
    CHECK (reviewer_id IS NULL OR reviewer_id<>requester_id),
    CHECK (status NOT IN ('approved','in_use','closed','failed') OR reviewer_id IS NOT NULL)
);
CREATE INDEX cmp_console_grants_project_recent
    ON cmp_instance_console_grants (tenant_id,project_id,created_at DESC,grant_id DESC);

ALTER TABLE cmp_project_audit DROP CONSTRAINT cmp_project_audit_category_check;
ALTER TABLE cmp_project_audit ADD CONSTRAINT cmp_project_audit_category_check
    CHECK (category IN ('account','ownership','operation','approval','resolution','observation','catalog','alert','console_access'));

CREATE FUNCTION cmp_capture_instance_console_grant() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' OR NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO cmp_project_audit (tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,outcome,revision,occurred_at)
        VALUES (NEW.tenant_id,NEW.project_id,'console_access',NEW.status,NEW.grant_id::text,'instance',
                NEW.account_id,NEW.region,NEW.actor_id,NEW.purpose,NEW.revision,clock_timestamp());
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_instance_console_grant_audit AFTER INSERT OR UPDATE OF status ON cmp_instance_console_grants
    FOR EACH ROW EXECUTE FUNCTION cmp_capture_instance_console_grant();
