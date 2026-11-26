package controller

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/inventory"
)

//go:embed 0004_inventory_schedules.sql
var schema string

//go:embed 0036_network_asset_schedules.sql
var networkAssetSchedulesSchema string

//go:embed 0041_snapshot_schedules.sql
var snapshotSchedulesSchema string

var ErrStale = errors.New("inventory schedule lease expired or was replaced")

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0004_inventory_schedules", schema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0036_network_asset_schedules", networkAssetSchedulesSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0041_snapshot_schedules", snapshotSchedulesSchema)
}

type job struct {
	key           inventory.Key
	provider      string
	credentialRef string
	id, fence     int64
	interval      time.Duration
	failures      int
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) Store { return Store{db: db} }

func (s Store) claim(ctx context.Context, lease time.Duration) (job, error) {
	var j job
	var period int64
	err := s.db.QueryRowContext(ctx, `WITH due AS (
		SELECT s.schedule_id,a.provider,a.credential_ref
		FROM cmp_inventory_schedules AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		WHERE s.enabled AND s.next_at<=statement_timestamp() AND (s.lease_until IS NULL OR s.lease_until<=statement_timestamp())
		  AND a.mode IN ('read_only','write_enabled')
		ORDER BY s.next_at,s.schedule_id LIMIT 1 FOR UPDATE OF s SKIP LOCKED
	)
	UPDATE cmp_inventory_schedules AS s
	SET fence=s.fence+1,lease_until=clock_timestamp()+$1*interval '1 millisecond'
	FROM due WHERE s.schedule_id=due.schedule_id
	RETURNING s.schedule_id,s.fence,s.tenant_id,s.account_id,s.region,s.kind,
		due.provider,due.credential_ref,s.interval_seconds,s.failures`, lease.Milliseconds()).
		Scan(&j.id, &j.fence, &j.key.Tenant, &j.key.Account, &j.key.Region, &j.key.Kind,
			&j.provider, &j.credentialRef, &period, &j.failures)
	j.interval = time.Duration(period) * time.Second
	return j, err
}

func (s Store) finish(ctx context.Context, j job, outcome string, delay time.Duration, failures int) error {
	res, err := s.db.ExecContext(ctx, `UPDATE cmp_inventory_schedules
		SET last_outcome=$3,last_finished_at=clock_timestamp(),next_at=clock_timestamp()+$4*interval '1 millisecond',
		    lease_until=NULL,failures=$5
		WHERE schedule_id=$1 AND fence=$2 AND lease_until>clock_timestamp()`,
		j.id, j.fence, outcome, delay.Milliseconds(), failures)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err == nil && count != 1 {
		return ErrStale
	}
	return err
}

func (s Store) release(ctx context.Context, j job) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE cmp_inventory_schedules SET lease_until=NULL WHERE schedule_id=$1 AND fence=$2", j.id, j.fence)
	return err
}
