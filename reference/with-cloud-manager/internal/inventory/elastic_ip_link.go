package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func LockElasticIPScope(ctx context.Context, tx *sql.Tx, scopeID int64) error {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT scope_id FROM cmp_inventory_scopes
		WHERE scope_id=$1 AND kind='elastic-ip' FOR NO KEY UPDATE`, scopeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	return err
}

func RecordElasticIPBinding(ctx context.Context, tx *sql.Tx, scopeID int64, object provider.Object) error {
	links, err := json.Marshal(append([]provider.Link{}, object.Links...))
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRowContext(ctx, `UPDATE cmp_inventory_objects AS o
		SET status=$3,links=$4,observed_at=statement_timestamp(),missing_generation=NULL,removed_at=NULL,
			seen_generation=GREATEST(o.seen_generation,s.generation)
		FROM cmp_inventory_scopes AS s WHERE o.scope_id=s.scope_id AND (o.scope_id,o.external_id)=($1,$2)
		AND s.kind='elastic-ip' AND o.deletion_confirmed_at IS NULL RETURNING o.external_id`,
		scopeID, object.ID, object.Status, string(links)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET running=false,fence=fence+1,cursor='',pages=0
		WHERE scope_id=$1 AND running`, scopeID)
	return err
}
