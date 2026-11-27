package alerts

import (
	"context"
	"database/sql"
	_ "embed"

	"with-cloud-manager/internal/dbschema"
)

const OperationQueueMigration = "0152_operation_queue_alerts"

//go:embed 0152_operation_queue_alerts.sql
var operationQueueSchema string

func MigrateOperationQueue(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, OperationQueueMigration, operationQueueSchema)
}

// EvaluateOperationQueueAlerts 在已领取的项目检查事务内写入队列告警。
func EvaluateOperationQueueAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	_, err := tx.ExecContext(ctx, `WITH candidates AS MATERIALIZED (
		SELECT DISTINCT account_id,state,CASE WHEN issue='anomaly' THEN 'operation_queue_anomaly'
			ELSE 'operation_queue_recovery' END AS rule
		FROM cmp_operation_queue_snapshot WHERE (tenant_id,project_id)=($1,$2)
		AND issue IS NOT NULL
	), resolved AS (
		UPDATE cmp_project_alerts AS a SET resolved_at=GREATEST(statement_timestamp(),a.opened_at,a.acknowledged_at),revision=a.revision+1
		WHERE (a.tenant_id,a.project_id)=($1,$2) AND a.resolved_at IS NULL
		AND a.rule IN ('operation_queue_anomaly','operation_queue_recovery')
		AND NOT EXISTS (SELECT FROM candidates AS c WHERE (c.rule,c.account_id)=(a.rule,a.subject_id))
	)
	INSERT INTO cmp_project_alerts (tenant_id,project_id,rule,subject_id,resource_kind,account_id,region,source_status)
	SELECT $1,$2,c.rule,c.account_id,'operation-queue',c.account_id,'',c.state FROM candidates AS c
	ON CONFLICT (tenant_id,project_id,rule,subject_id) WHERE resolved_at IS NULL DO NOTHING`, tenant, project)
	return err
}
