CREATE TABLE cmp_tenants (
    tenant_id text PRIMARY KEY
);

CREATE TABLE cmp_projects (
    tenant_id text NOT NULL REFERENCES cmp_tenants,
    project_id text NOT NULL,
    PRIMARY KEY (tenant_id, project_id)
);

CREATE TABLE cmp_principals (
    tenant_id text NOT NULL REFERENCES cmp_tenants,
    principal_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('human', 'service')),
    issuer text NOT NULL,
    subject text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY (tenant_id, principal_id),
    UNIQUE (tenant_id, issuer, subject)
);

CREATE TABLE cmp_memberships (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    principal_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('viewer', 'operator', 'approver', 'admin')),
    active boolean NOT NULL DEFAULT true,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    PRIMARY KEY (tenant_id, project_id, principal_id),
    FOREIGN KEY (tenant_id, project_id) REFERENCES cmp_projects,
    FOREIGN KEY (tenant_id, principal_id) REFERENCES cmp_principals
);

CREATE TABLE cmp_cloud_accounts (
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    project_id text NOT NULL,
    provider text NOT NULL CHECK (provider <> ''),
    remote_identity text,
    credential_ref text NOT NULL CHECK (credential_ref <> ''),
    credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision > 0),
    mode text NOT NULL DEFAULT 'pending' CHECK (mode IN ('pending', 'read_only', 'write_enabled', 'suspended', 'revoked')),
    CHECK (mode NOT IN ('read_only', 'write_enabled') OR (remote_identity IS NOT NULL AND remote_identity <> '')),
    PRIMARY KEY (tenant_id, account_id),
    UNIQUE (provider, remote_identity),
    FOREIGN KEY (tenant_id, project_id) REFERENCES cmp_projects
);

CREATE TABLE cmp_cloud_account_grants (
    tenant_id text NOT NULL,
    account_id text NOT NULL,
    project_id text NOT NULL,
    allow_write boolean NOT NULL DEFAULT false,
    PRIMARY KEY (tenant_id, account_id, project_id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES cmp_cloud_accounts,
    FOREIGN KEY (tenant_id, project_id) REFERENCES cmp_projects
);

CREATE FUNCTION cmp_membership_next_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.role IS DISTINCT FROM OLD.role OR NEW.active IS DISTINCT FROM OLD.active THEN
        NEW.revision := OLD.revision + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_membership_revision
    BEFORE UPDATE OF role, active ON cmp_memberships
    FOR EACH ROW EXECUTE FUNCTION cmp_membership_next_revision();

CREATE FUNCTION cmp_cloud_account_guard_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.provider <> OLD.provider
        OR (OLD.remote_identity IS NOT NULL AND NEW.remote_identity IS DISTINCT FROM OLD.remote_identity) THEN
        RAISE EXCEPTION 'cloud account identity cannot change' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.credential_revision < OLD.credential_revision THEN
        RAISE EXCEPTION 'cloud credential revision cannot decrease' USING ERRCODE = 'check_violation';
    END IF;
    IF (NEW.credential_ref IS DISTINCT FROM OLD.credential_ref OR NEW.mode IS DISTINCT FROM OLD.mode)
        AND NEW.credential_revision = OLD.credential_revision THEN
        NEW.credential_revision := OLD.credential_revision + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER cmp_cloud_account_guard
    BEFORE UPDATE ON cmp_cloud_accounts
    FOR EACH ROW EXECUTE FUNCTION cmp_cloud_account_guard_identity();
