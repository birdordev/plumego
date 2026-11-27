package controller

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/inventory"
)

const (
	BucketEncryptionScheduleKind = "object-bucket-encryption"
	BucketVersioningScheduleKind = "object-bucket-versioning"
)

func isBucketSettingSchedule(kind string) bool {
	return kind == BucketEncryptionScheduleKind || kind == BucketVersioningScheduleKind
}

func (s Store) PrepareBucketSettingSweep(ctx context.Context, task Task,
	collect func(context.Context, string) (inventory.BucketSettingBatch, error),
) (Publish, error) {
	if !isBucketSettingSchedule(task.ScheduleKind) {
		return nil, inventory.ErrInvalidQuery
	}
	var cursor string
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN bucket_cursor_revision=$6 THEN bucket_cursor ELSE '' END
		FROM cmp_inventory_schedules
		WHERE (schedule_id,fence,tenant_id,account_id,kind)=($1,$2,$3,$4,$5)
		AND enabled AND lease_until>statement_timestamp()`,
		task.Lease.ID, task.Lease.Fence, task.Key.Tenant, task.Key.Account, task.ScheduleKind, task.CredentialRevision).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, inventory.ErrStale
	}
	if err != nil {
		return nil, err
	}
	batch, err := collect(ctx, cursor)
	if batch.Publish == nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT credential_revision FROM cmp_cloud_accounts
			WHERE (tenant_id,account_id,credential_ref,provider)=($1,$2,$3,$4)
			AND credential_revision=$5 AND mode IN ('read_only','write_enabled') AND remote_identity<>'' FOR SHARE`,
			task.Key.Tenant, task.Key.Account, task.CredentialRef, task.Provider, task.CredentialRevision).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			return inventory.ErrStale
		}
		if err != nil {
			return err
		}
		rejected, err := batch.Publish(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE cmp_inventory_schedules
			SET bucket_cursor=$2,bucket_cursor_revision=$3,bucket_batch_rejected=$4,
				next_at=CASE WHEN $2<>'' AND last_outcome='success'
					THEN LEAST(next_at,clock_timestamp()+interval '5 seconds') ELSE next_at END,
				last_success_at=CASE WHEN $2='' AND last_outcome='success'
					THEN last_finished_at ELSE last_success_at END
			WHERE schedule_id=$1`, task.Lease.ID, batch.Next, revision, rejected)
		return err
	}, err
}
