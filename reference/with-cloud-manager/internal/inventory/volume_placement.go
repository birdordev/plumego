package inventory

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

func CheckVolumePlacement(ctx context.Context, tx *sql.Tx, tenant, account, region, zone string, credentialRevision int64) error {
	var found string
	err := tx.QueryRowContext(ctx, `SELECT o.external_id FROM cmp_inventory_scopes AS s
		JOIN cmp_inventory_objects AS o USING(scope_id)
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,'zone',$4)
		  AND s.credential_revision=$5 AND o.removed_at IS NULL AND o.missing_generation IS NULL
		  AND lower(o.status) IN ('available','observed') FOR SHARE OF o`,
		tenant, account, region, zone, credentialRevision).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrAccountStale
	}
	return err
}
