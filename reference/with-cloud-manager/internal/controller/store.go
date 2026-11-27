package controller

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0004_inventory_schedules.sql
var schema string

//go:embed 0036_network_asset_schedules.sql
var networkAssetSchedulesSchema string

//go:embed 0041_snapshot_schedules.sql
var snapshotSchedulesSchema string

//go:embed 0057_load_balancer_schedules.sql
var loadBalancerSchedulesSchema string

//go:embed 0068_dns_zone_schedules.sql
var dnsZoneSchedulesSchema string

//go:embed 0077_database_schedules.sql
var databaseSchedulesSchema string

//go:embed 0079_object_bucket_schedules.sql
var objectBucketSchedulesSchema string

//go:embed 0081_cache_schedules.sql
var cacheSchedulesSchema string

//go:embed 0083_kubernetes_schedules.sql
var kubernetesSchedulesSchema string

//go:embed 0085_cluster_node_pool_schedules.sql
var clusterNodePoolSchedulesSchema string

//go:embed 0087_cluster_workload_schedules.sql
var clusterWorkloadSchedulesSchema string

//go:embed 0089_cluster_workload_watch_schedules.sql
var clusterWorkloadWatchSchedulesSchema string

//go:embed 0117_redfish_schedules.sql
var redfishSchedulesSchema string

//go:embed 0136_inventory_schedule_config.sql
var inventoryScheduleConfigSchema string

//go:embed 0137_inventory_account_dispatch.sql
var inventoryAccountDispatchSchema string

//go:embed 0138_inventory_tenant_dispatch.sql
var inventoryTenantDispatchSchema string

const RedfishScheduleMigration = "0117_redfish_schedules"
const ScheduleConfigMigration = "0136_inventory_schedule_config"
const AccountDispatchMigration = "0137_inventory_account_dispatch"
const TenantDispatchMigration = "0138_inventory_tenant_dispatch"

var ErrStale = errors.New("inventory schedule lease expired or was replaced")

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0004_inventory_schedules", schema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0036_network_asset_schedules", networkAssetSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0041_snapshot_schedules", snapshotSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0057_load_balancer_schedules", loadBalancerSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0068_dns_zone_schedules", dnsZoneSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0077_database_schedules", databaseSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0079_object_bucket_schedules", objectBucketSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0081_cache_schedules", cacheSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0083_kubernetes_schedules", kubernetesSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0085_cluster_node_pool_schedules", clusterNodePoolSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0087_cluster_workload_schedules", clusterWorkloadSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0089_cluster_workload_watch_schedules", clusterWorkloadWatchSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0099_idle_schedules", idleScheduleSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, RedfishScheduleMigration, redfishSchedulesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, ScheduleConfigMigration, inventoryScheduleConfigSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, AccountDispatchMigration, inventoryAccountDispatchSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, TenantDispatchMigration, inventoryTenantDispatchSchema)
}

const (
	NodePoolScheduleKind      = "kubernetes-node-pools"
	WorkloadScheduleKind      = "kubernetes-workloads"
	WorkloadWatchScheduleKind = "kubernetes-workload-watch"
)

type Task struct {
	ScheduleKind       string
	Key                inventory.Key
	TargetID           string
	Provider           string
	CredentialRef      string
	CredentialRevision int64
	Lease              inventory.ScheduleLease
}

type job struct {
	task           Task
	id, fence      int64
	interval       time.Duration
	failures       int
	accountLimited bool
}

type Store struct {
	db              *sql.DB
	observeDispatch func(DispatchObservation)
}

func NewStore(db *sql.DB) Store { return Store{db: db} }

func (s Store) claim(ctx context.Context, lease time.Duration, pool schedulePool) (job, error) {
	if pool == inventoryPool {
		return s.claimAccountSchedule(ctx, lease)
	}
	// 使用固定条件，让数据库能使用订阅任务的部分索引。
	kindFilter := "s.kind='kubernetes-workload-watch'"
	if pool == idleAnalysisPool {
		kindFilter = "s.kind='instance-idle'"
	}
	return scanLeasedSchedule(s.db.QueryRowContext(ctx, `WITH due AS (
		SELECT s.schedule_id,a.provider,a.credential_ref,a.credential_revision
		FROM cmp_inventory_schedules AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		WHERE s.enabled AND s.next_at<=statement_timestamp() AND (s.lease_until IS NULL OR s.lease_until<=statement_timestamp())
		  AND a.mode IN ('read_only','write_enabled') AND `+kindFilter+`
		ORDER BY s.next_at,s.schedule_id LIMIT 1 FOR UPDATE OF s SKIP LOCKED
	)
	UPDATE cmp_inventory_schedules AS s
	SET fence=s.fence+1,lease_until=clock_timestamp()+$1*interval '1 millisecond'
	FROM due WHERE s.schedule_id=due.schedule_id
	RETURNING s.schedule_id,s.fence,s.tenant_id,s.account_id,s.region,s.kind,s.target_id,
		due.provider,due.credential_ref,due.credential_revision,s.interval_seconds,s.failures`, lease.Milliseconds()))
}

func scanLeasedSchedule(row *sql.Row) (job, error) {
	var j job
	var period int64
	err := row.Scan(&j.id, &j.fence, &j.task.Key.Tenant, &j.task.Key.Account, &j.task.Key.Region, &j.task.ScheduleKind, &j.task.TargetID,
		&j.task.Provider, &j.task.CredentialRef, &j.task.CredentialRevision, &period, &j.failures)
	if err != nil {
		return job{}, err
	}
	j.task.Key.Kind = provider.Kind(j.task.ScheduleKind)
	switch j.task.ScheduleKind {
	case NodePoolScheduleKind, WorkloadScheduleKind, WorkloadWatchScheduleKind:
		j.task.Key.Kind = provider.KubernetesCluster
	case IdleScheduleKind:
		j.task.Key.Kind = provider.Instance
	}
	j.interval = time.Duration(period) * time.Second
	j.task.Lease = inventory.ScheduleLease{ID: j.id, Fence: j.fence}
	return j, nil
}

func (s Store) finish(ctx context.Context, j job, outcome string, delay time.Duration, failures int, publish Publish) error {
	if publish == nil && !j.accountLimited {
		return finishSchedule(ctx, s.db, j, outcome, delay, failures, false)
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		if err := finishSchedule(ctx, tx, j, outcome, delay, failures, publish != nil); err != nil {
			return err
		}
		if publish != nil {
			if err := publish(ctx, tx); err != nil {
				return err
			}
		}
		if j.accountLimited {
			return releaseAccountDispatch(ctx, tx, j, accountCooldown(outcome, delay))
		}
		return nil
	})
}

type scheduleExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func finishSchedule(ctx context.Context, db scheduleExecutor, j job, outcome string, delay time.Duration, failures int, publishing bool) error {
	res, err := db.ExecContext(ctx, `WITH stamp AS (SELECT clock_timestamp() AS at_time)
		UPDATE cmp_inventory_schedules
		SET last_outcome=$3,last_finished_at=stamp.at_time,next_at=stamp.at_time+$4*interval '1 millisecond',
		    lease_until=NULL,failures=$5,
		    last_success_at=CASE WHEN $3='success' AND target_id<>''
		      AND (kind<>'instance-idle' OR $11) THEN stamp.at_time ELSE last_success_at END
		FROM stamp
		WHERE schedule_id=$1 AND fence=$2 AND enabled AND lease_until>clock_timestamp()
		  AND (tenant_id,account_id,region,kind,target_id)=($6,$7,$8,$9,$10)`,
		j.id, j.fence, outcome, delay.Milliseconds(), failures, j.task.Key.Tenant, j.task.Key.Account, j.task.Key.Region, j.task.ScheduleKind, j.task.TargetID, publishing)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err == nil && count != 1 {
		return ErrStale
	}
	return err
}

func (s Store) release(ctx context.Context, j job, cooldown time.Duration) error {
	if !j.accountLimited {
		return releaseSchedule(ctx, s.db, j)
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		if err := releaseSchedule(ctx, tx, j); err != nil {
			return err
		}
		return releaseAccountDispatch(ctx, tx, j, cooldown)
	})
}

func releaseSchedule(ctx context.Context, db scheduleExecutor, j job) error {
	_, err := db.ExecContext(ctx,
		"UPDATE cmp_inventory_schedules SET lease_until=NULL WHERE schedule_id=$1 AND fence=$2", j.id, j.fence)
	return err
}
