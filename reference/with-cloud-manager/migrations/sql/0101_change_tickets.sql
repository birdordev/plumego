CREATE TABLE cmp_change_ticket_policies (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    actions text[] NOT NULL,
    verifier_id text NOT NULL,
    published_by text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (tenant_id,project_id,revision),
    FOREIGN KEY (tenant_id,project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id,verifier_id) REFERENCES cmp_principals,
    FOREIGN KEY (tenant_id,published_by) REFERENCES cmp_principals
);

ALTER TABLE cmp_instance_operations
    ADD COLUMN ticket_policy_revision bigint,
    ADD UNIQUE (tenant_id,project_id,operation_id),
    ADD FOREIGN KEY (tenant_id,project_id,ticket_policy_revision) REFERENCES cmp_change_ticket_policies;

ALTER TABLE cmp_principals ADD COLUMN ticket_auth_revision bigint NOT NULL DEFAULT 1;

CREATE FUNCTION cmp_change_ticket_principal_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.active IS DISTINCT FROM OLD.active OR NEW.kind IS DISTINCT FROM OLD.kind THEN
        NEW.ticket_auth_revision := OLD.ticket_auth_revision + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_change_ticket_principal_changed
    BEFORE UPDATE OF active,kind ON cmp_principals FOR EACH ROW
    EXECUTE FUNCTION cmp_change_ticket_principal_revision();

CREATE FUNCTION cmp_pin_change_ticket_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    SELECT policy.revision INTO NEW.ticket_policy_revision
    FROM (
        SELECT revision, actions FROM cmp_change_ticket_policies
        WHERE (tenant_id,project_id)=(NEW.tenant_id,NEW.project_id)
        ORDER BY revision DESC LIMIT 1
    ) AS policy
    WHERE NEW.action=ANY(policy.actions) OR '*'=ANY(policy.actions);
    IF NEW.ticket_policy_revision IS NOT NULL THEN
        NEW.status := 'awaiting_approval';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_operation_change_ticket
    BEFORE INSERT ON cmp_instance_operations FOR EACH ROW
    EXECUTE FUNCTION cmp_pin_change_ticket_policy();

CREATE TABLE cmp_change_ticket_links (
    operation_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    ticket_id text NOT NULL,
    UNIQUE (tenant_id,project_id,ticket_id),
    FOREIGN KEY (tenant_id,project_id,operation_id) REFERENCES cmp_instance_operations (tenant_id,project_id,operation_id)
);

CREATE TABLE cmp_change_ticket_receipts (
    operation_id text NOT NULL REFERENCES cmp_change_ticket_links,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    decision text NOT NULL CHECK (decision IN ('approved','revoked')),
    valid_until timestamptz,
    verifier_revision bigint NOT NULL CHECK (verifier_revision > 0),
    principal_revision bigint NOT NULL CHECK (principal_revision > 0),
    recorded_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    PRIMARY KEY (operation_id,source_revision),
    CHECK ((decision='approved')=(valid_until IS NOT NULL))
);

CREATE FUNCTION cmp_change_ticket_valid(op cmp_instance_operations) RETURNS boolean LANGUAGE sql VOLATILE AS $$
    SELECT COALESCE((
        SELECT receipt.decision='approved' AND receipt.valid_until>clock_timestamp()
            AND member.active AND member.role='viewer' AND member.revision=receipt.verifier_revision
            AND verifier.active AND verifier.kind='service' AND verifier.ticket_auth_revision=receipt.principal_revision
        FROM cmp_change_ticket_policies AS policy
        JOIN cmp_principals AS verifier ON (verifier.tenant_id,verifier.principal_id)=(policy.tenant_id,policy.verifier_id)
        JOIN cmp_memberships AS member ON (member.tenant_id,member.project_id,member.principal_id)=(policy.tenant_id,policy.project_id,policy.verifier_id)
        JOIN LATERAL (
            SELECT decision,valid_until,verifier_revision,principal_revision FROM cmp_change_ticket_receipts
            WHERE operation_id=(op).operation_id ORDER BY source_revision DESC LIMIT 1
        ) AS receipt ON true
        WHERE (policy.tenant_id,policy.project_id,policy.revision)=((op).tenant_id,(op).project_id,(op).ticket_policy_revision)
    ),false);
$$;

CREATE FUNCTION cmp_guard_change_ticket_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT cmp_change_ticket_valid(OLD) THEN
        RAISE EXCEPTION 'current external change approval required' USING ERRCODE='PT409';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_guard_operation_change_ticket
    BEFORE UPDATE OF status ON cmp_instance_operations FOR EACH ROW
    WHEN (OLD.ticket_policy_revision IS NOT NULL AND
        ((OLD.status='awaiting_approval' AND NEW.status='queued') OR (OLD.status='queued' AND NEW.status='applying')))
    EXECUTE FUNCTION cmp_guard_change_ticket_dispatch();

CREATE FUNCTION cmp_guard_change_ticket_creation_replay() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT FROM cmp_instance_operations AS op WHERE op.operation_id=NEW.operation_id
        AND op.ticket_policy_revision IS NOT NULL AND NOT cmp_change_ticket_valid(op)) THEN
        RAISE EXCEPTION 'current external change approval required' USING ERRCODE='PT409';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_guard_launch_change_ticket
    BEFORE UPDATE OF replayed_at ON cmp_instance_launches FOR EACH ROW
    WHEN (OLD.replayed_at IS NULL AND NEW.replayed_at IS NOT NULL)
    EXECUTE FUNCTION cmp_guard_change_ticket_creation_replay();

ALTER TABLE cmp_operation_event_sinks ADD COLUMN include_change_tickets boolean NOT NULL DEFAULT false;
