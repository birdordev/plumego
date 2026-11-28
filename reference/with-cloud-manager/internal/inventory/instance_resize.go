package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func InstanceResizeTarget(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (size, state string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT o.instance_type,o.status
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND s.credential_revision=$3 AND o.instance_type<>''`,
		target.ScopeID, id, target.CredentialRevision).Scan(&size, &state)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrAccountStale
	}
	return size, state, err
}

func RecordObservedInstanceType(ctx context.Context, tx *sql.Tx, scopeID int64, change provider.InstanceResize, observed string) error {
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_inventory_objects
		SET instance_type=$4,status=$5,observed_at=statement_timestamp()
		WHERE (scope_id,external_id)=($1,$2) AND instance_type IN ($3,$4)
		  AND removed_at IS NULL AND missing_generation IS NULL RETURNING external_id`,
		scopeID, change.ID, change.CurrentType, change.TargetType, observed).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	return restartActiveInventoryScan(ctx, tx, scopeID)
}
