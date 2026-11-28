package identity

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
)

func Migrate(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0001_identity")
}

type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Store struct {
	db Querier
}

func NewStore(db Querier) Store {
	return Store{db: db}
}

const activeMembership = `
	FROM cmp_memberships AS m
	JOIN cmp_principals AS p USING (tenant_id, principal_id)`

const currentScope = `
	WHERE m.tenant_id=$1 AND m.project_id=$2 AND m.principal_id=$3
	  AND m.active AND p.active`

func (s Store) AuthorizeProject(ctx context.Context, scope Scope, permission Permission) (Membership, error) {
	var member Membership
	err := s.db.QueryRowContext(ctx, "SELECT m.role, m.revision"+activeMembership+currentScope,
		scope.Tenant, scope.Project, scope.Principal).Scan(&member.Role, &member.Revision)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !member.Role.allows(permission)) {
		return Membership{}, ErrForbidden
	}
	return member, err
}

func (s Store) AuthorizeAccount(ctx context.Context, scope Scope, id string, permission Permission) (AccountAccess, error) {
	if permission != View && permission != Execute && permission != ExecuteBareMetal {
		return AccountAccess{}, ErrForbidden
	}
	var access AccountAccess
	var role Role
	var mode string
	var writeGrant bool
	err := s.db.QueryRowContext(ctx, `SELECT m.role, m.revision, a.provider,
			a.remote_identity, a.credential_revision, a.mode,
			(a.project_id=m.project_id OR COALESCE(g.allow_write, false))`+activeMembership+`
		JOIN cmp_cloud_accounts AS a ON a.tenant_id=m.tenant_id AND a.account_id=$4
		LEFT JOIN cmp_cloud_account_grants AS g ON g.tenant_id=a.tenant_id
		  AND g.account_id=a.account_id AND g.project_id=m.project_id`+currentScope+`
		AND (a.project_id=m.project_id OR g.project_id IS NOT NULL)
		AND a.mode IN ('read_only', 'write_enabled')`, scope.Tenant, scope.Project, scope.Principal, id).
		Scan(&role, &access.MembershipRevision, &access.Provider,
			&access.RemoteIdentity, &access.CredentialRevision, &mode, &writeGrant)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountAccess{}, ErrForbidden
	}
	if err != nil {
		return AccountAccess{}, err
	}
	if !role.allows(permission) || (permission == Execute && (mode != "write_enabled" || !writeGrant || access.Provider == provider.VSphere || access.Provider == provider.Redfish)) ||
		(permission == ExecuteBareMetal && (mode != "write_enabled" || !writeGrant || access.Provider != provider.Redfish)) {
		return AccountAccess{}, ErrForbidden
	}
	return access, nil
}
