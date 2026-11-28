package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"
	"with-cloud-manager/internal/identity"
)

//go:embed 0154_operation_observation_queue_repair.sql
var observationQueueRepairSchema string

const ObservationQueueRepairMigration = "0154_operation_observation_queue_repair"

const observationQueueRestarted = "queue_observation_restarted"

func supportsObservationRetry(action string) bool {
	switch action {
	case "start", "stop", "reboot", "create", "create-network", "create-subnet", "create-security-group", "create-security-group-rule",
		"create-elastic-ip", createVolume, "create-snapshot", "delete-snapshot", "restore-snapshot", "rollback-snapshot", "create-image",
		"copy-image", "delete-image", "delete-network", "delete-subnet", "delete-security-group", "revoke-security-group-rule",
		"release-elastic-ip", "bind-elastic-ip", "unbind-elastic-ip", "rename-load-balancer", "rename-load-balancer-listener",
		"rename-load-balancer-target-group", "set-load-balancer-backend-weight", "detach-load-balancer-backend", "attach-load-balancer-backend",
		"set-dns-record-status", "create-dns-record", "delete-dns-record", "scale-workload", "rename-database-instance", "rename-cache-instance",
		resizeVolume, resizeInstance, setBucketVersioning, releaseVolume, "retire-instance", "power-on-bare-metal", "shutdown-bare-metal", "stage-bare-metal-pxe", "cancel-bare-metal-pxe":
		return true
	default:
		return false
	}
}

func lockObservationTask(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT t.status FROM cmp_operation_tasks AS t
		JOIN cmp_instance_operations AS o ON (t.tenant_id,t.id)=(o.tenant_id,o.operation_id)
		WHERE (o.tenant_id,o.project_id,o.operation_id,t.topic)=($1,$2,$3,$4) FOR UPDATE OF t`,
		actor.Tenant, actor.Project, id, topic).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return status, err
}

func observationQueueIssue(ctx context.Context, tx *sql.Tx, actor identity.Scope, op Operation) (string, error) {
	if (op.Status != "applying" && op.Status != "observing") || !supportsObservationRetry(op.Action) {
		return "", nil
	}
	var reason string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT issue_reason FROM cmp_operation_queue_snapshot
		WHERE (tenant_id,project_id,operation_id)=($1,$2,$3) AND issue='anomaly'),'')`, actor.Tenant, actor.Project, op.ID).Scan(&reason)
	return reason, err
}

func (s Store) repairObservationTask(ctx context.Context, tx *sql.Tx, actor identity.Scope, op Operation, missing bool) error {
	if missing {
		// 缺失任务也沿用原操作 ID。主键冲突表示队列已变化或存在坐标冲突。
		if err := s.enqueue(ctx, tx, actor.Tenant, op); err != nil {
			if errors.Is(err, mq.ErrDuplicateTask) {
				return ErrConflict
			}
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `WITH previous AS (
		SELECT COALESCE(max(attempt_no),0) AS last_attempt FROM cmp_operation_attempts WHERE task_id=$1
	)
	UPDATE cmp_operation_tasks AS t SET status='queued',payload=convert_to(t.id,'UTF8'),
		attempts=GREATEST(t.attempts,p.last_attempt),max_attempts=GREATEST(t.attempts,p.last_attempt)+$4,
		available_at=statement_timestamp(),updated_at=statement_timestamp(),lease_owner=NULL,lease_until=NULL,expires_at=NULL
	FROM previous AS p WHERE (t.id,t.tenant_id,t.topic)=($1,$2,$3)`, op.ID, actor.Tenant, topic, operationAttemptBudget(op.Action))
	return err
}
