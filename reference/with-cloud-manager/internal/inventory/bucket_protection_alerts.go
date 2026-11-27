package inventory

import (
	"context"
	"database/sql"
	_ "embed"
)

const bucketProtectionAlertBase = `WITH observations AS (
	SELECT s.account_id,
		CASE WHEN schedule.kind='object-bucket-encryption' THEN 'bucket_encryption_unconfigured'
			 ELSE 'bucket_versioning_inactive' END AS rule,
		CASE WHEN (` + currentObjectBucket + `) AND (` + ownershipSource + `)<>'pending' THEN
			CASE WHEN schedule.kind='object-bucket-encryption' AND e.observed_at >= transaction_timestamp()-make_interval(secs=>$3) THEN e.state
				 WHEN schedule.kind='object-bucket-versioning' AND v.observed_at >= transaction_timestamp()-make_interval(secs=>$3) THEN v.state END
		END AS state
	FROM cmp_inventory_scopes AS s
	JOIN cmp_inventory_objects AS o USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	JOIN cmp_inventory_schedules AS schedule ON (schedule.tenant_id,schedule.account_id,schedule.region)=(s.tenant_id,s.account_id,'global')
		AND schedule.target_id='' AND schedule.enabled AND schedule.kind IN ('object-bucket-encryption','object-bucket-versioning')` +
	bucketProtectionSnapshotJoins + directInventoryOwnershipJoins + `
	WHERE s.tenant_id=$1 AND s.kind='object-bucket' AND s.region='global' AND o.removed_at IS NULL
		AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs') AND (` + projectedOwner + `)=$2
)`

//go:embed bucket_protection_alerts.sql
var bucketProtectionAlertChanges string

func EvaluateBucketProtectionAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	_, err := tx.ExecContext(ctx, bucketProtectionAlertBase+bucketProtectionAlertChanges, tenant, project, DefaultBucketProtectionWindow)
	return err
}
