package inventory

import (
	"context"
	"database/sql"

	"with-cloud-manager/migrations"
)

const databaseProtectionAlertBase = `WITH limits AS (
	SELECT transaction_timestamp()-make_interval(secs=>$3::integer) AS observed_after,
		transaction_timestamp()-make_interval(secs=>$4::integer) AS evidence_after
), visible AS (
	SELECT schedule.kind AS schedule_kind,
		s.account_id AS account,s.region,a.provider,o.external_id AS id,
		o.database_instance->>'product' AS product,o.database_instance->>'engine' AS engine,
		COALESCE((` + currentObservedDatabase + `) AND (` + ownershipSource + `)<>'pending',false) AS inventory_available,
		b.observed_at AS backup_observed_at,b.latest_ready_started_at,
		w.observed_at AS restore_observed_at,w.latest_window_end_at
	FROM cmp_inventory_schedules AS schedule
	JOIN cmp_inventory_scopes AS s ON (s.tenant_id,s.account_id,s.region)=(schedule.tenant_id,schedule.account_id,schedule.region)
		AND s.kind='database-instance'
	JOIN cmp_inventory_objects AS o ON (o.scope_id,o.external_id)=(s.scope_id,schedule.target_id)
	JOIN cmp_cloud_accounts AS a ON (a.tenant_id,a.account_id)=(s.tenant_id,s.account_id)` +
	databaseProtectionSnapshots + directInventoryOwnershipJoins + `
	WHERE schedule.tenant_id=$1 AND schedule.enabled AND schedule.kind IN ('database-backups','database-restore-windows')
		AND o.removed_at IS NULL AND o.database_instance IS NOT NULL
		AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs') AND (` + projectedOwner + `)=$2
)`

func EvaluateDatabaseProtectionAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	_, err := tx.ExecContext(ctx, databaseProtectionAlertBase+migrations.DatabaseProtectionPosture()+migrations.DatabaseProtectionAlerts(),
		tenant, project, DefaultDatabaseProtectionWindow, DefaultDatabaseProtectionWindow)
	return err
}
