package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type DatabaseAssetQuery struct {
	Tenant, Project, Account, Region, ID string
}

func (q DatabaseAssetQuery) valid() bool {
	return validConfigurationQueryValue(q.Tenant, 256) && validConfigurationQueryValue(q.Project, 256) &&
		validDatabaseObservationKey(Key{Tenant: q.Tenant, Account: q.Account, Region: q.Region, Kind: provider.DatabaseInstance}, q.ID)
}

const currentObservedDatabase = `s.kind='database-instance' AND o.database_instance IS NOT NULL
	AND o.removed_at IS NULL AND o.missing_generation IS NULL
	AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision
	AND a.remote_identity<>''`

const selectObservedDatabase = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE (s.tenant_id,s.account_id,s.region,o.external_id,a.credential_revision)=($1,$2,$3,$4,$5) AND ` + currentObservedDatabase

const ownedObservedDatabase = inventoryOwnershipJoins +
	" WHERE " + assetVisibility + " AND " + currentObservedDatabase + " AND (s.account_id,s.region,o.external_id)=($3,$4,$5)"

func ResolveDatabaseObservationTarget(ctx context.Context, tx *sql.Tx, q DatabaseAssetQuery) (string, provider.DatabaseDetails, error) {
	if !q.valid() {
		return "", provider.DatabaseDetails{}, identity.ErrAccountInvalid
	}
	var vendor string
	var details provider.DatabaseDetails
	err := tx.QueryRowContext(ctx, `SELECT a.provider,o.database_instance->>'product',o.database_instance->>'engine'
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)`+ownedObservedDatabase,
		q.Tenant, q.Project, q.Account, q.Region, q.ID).Scan(&vendor, &details.Product, &details.Engine)
	if errors.Is(err, sql.ErrNoRows) {
		return "", provider.DatabaseDetails{}, identity.ErrForbidden
	}
	return vendor, details, err
}

type databaseObservation struct {
	key                     Key
	id                      string
	revision, scopeID       int64
	scope                   provider.Scope
	details                 provider.DatabaseDetails
	parentObserved, started time.Time
}

func validDatabaseObservationKey(key Key, id string) bool {
	return key.Kind == provider.DatabaseInstance && validConfigurationQueryValue(key.Tenant, 256) &&
		validConfigurationQueryValue(key.Account, 256) && validConfigurationQueryValue(key.Region, 128) && validConfigurationQueryValue(id, 128)
}

func (s Store) databaseObservation(ctx context.Context, key Key, id string, revision int64) (databaseObservation, error) {
	if revision < 1 || !validDatabaseObservationKey(key, id) {
		return databaseObservation{}, ErrInvalidQuery
	}
	target := databaseObservation{key: key, id: id, revision: revision, scope: provider.Scope{Kind: key.Kind, Region: key.Region}}
	err := s.db.QueryRowContext(ctx, `SELECT s.scope_id,a.provider,a.remote_identity,o.database_instance->>'product',
		o.database_instance->>'engine',o.observed_at,clock_timestamp()`+selectObservedDatabase,
		key.Tenant, key.Account, key.Region, id, revision).
		Scan(&target.scopeID, &target.scope.Provider, &target.scope.RemoteIdentity, &target.details.Product, &target.details.Engine,
			&target.parentObserved, &target.started)
	if errors.Is(err, sql.ErrNoRows) {
		return databaseObservation{}, ErrStale
	}
	return target, err
}

func (target databaseObservation) lock(ctx context.Context, tx *sql.Tx) error {
	var found int64
	err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+selectObservedDatabase+
		` AND s.scope_id=$6 AND o.observed_at=$7 AND a.remote_identity=$8 AND o.database_instance->>'product'=$9
		AND o.database_instance->>'engine'=$10 FOR SHARE OF a,s,o`,
		target.key.Tenant, target.key.Account, target.key.Region, target.id, target.revision,
		target.scopeID, target.parentObserved, target.scope.RemoteIdentity, target.details.Product, target.details.Engine).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	return err
}

func checkDatabaseObservationWrite(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrStale
	}
	return err
}
