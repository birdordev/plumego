package inventory

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func instanceRetirementActive(ctx context.Context, tx *sql.Tx, kind provider.Kind, scopeID int64, id string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM cmp_instance_retirement_reservations WHERE (instance_scope,instance_id)=($1,$2))`
	if kind == provider.Volume {
		query = `SELECT EXISTS(SELECT 1 FROM cmp_instance_retirement_reservations WHERE (disk_scope,disk_id)=($1,$2))`
	}
	var active bool
	err := tx.QueryRowContext(ctx, query, scopeID, id).Scan(&active)
	return active, err
}

func ReserveInstanceRetirement(ctx context.Context, tx *sql.Tx, scope identity.Scope, operation string, instance, disk MutationTarget, id, diskID string) (bool, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_retirement_reservations
		(operation_id,tenant_id,project_id,instance_scope,instance_id,disk_scope,disk_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`,
		operation, scope.Tenant, scope.Project, instance.ScopeID, id, disk.ScopeID, diskID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
