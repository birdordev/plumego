package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0108_cache_rename_reservations.sql
var cacheRenameReservationsSchema string

func LockCacheScope(ctx context.Context, tx *sql.Tx, tenant, account, region string) (int64, error) {
	return lockInventoryScope(ctx, tx, tenant, account, region, provider.CacheInstance)
}

func CacheRenameTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (name, product string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT o.name,o.cache_instance->>'product'
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND s.credential_revision=$3
		  AND o.cache_instance->>'product' IS NOT NULL AND o.cache_instance->>'engine'='redis'`,
		target.ScopeID, id, target.CredentialRevision).Scan(&name, &product)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	return name, product, err
}

func cacheRenameActive(ctx context.Context, tx *sql.Tx, scopeID int64, id string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_cache_rename_reservations
		WHERE (scope_id,external_id)=($1,$2))`, scopeID, id).Scan(&active)
	return active, err
}
