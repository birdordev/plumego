package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"
)

type ScheduleLease struct {
	ID, Fence int64
}

func (s Store) ClaimScheduled(ctx context.Context, key Key, lease time.Duration, revision int64, schedule ScheduleLease) (Claim, error) {
	if revision <= 0 || schedule.ID <= 0 || schedule.Fence <= 0 {
		return Claim{}, ErrUnavailable
	}
	var claim Claim
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var err error
		claim, err = claimScope(ctx, tx, key, lease, revision)
		if err != nil {
			return err
		}
		claim.schedule, claim.key = schedule, key
		return checkScheduleLease(ctx, tx, claim)
	})
	if err != nil {
		return Claim{}, err
	}
	return claim, nil
}

func checkScheduleLease(ctx context.Context, db scopeQuerier, claim Claim) error {
	var id int64
	err := db.QueryRowContext(ctx, `SELECT schedule_id FROM cmp_inventory_schedules
		WHERE schedule_id=$1 AND fence=$2 AND enabled AND lease_until>clock_timestamp() AND target_id=''
		  AND (tenant_id,account_id,region,kind)=($3,$4,$5,$6) FOR SHARE`,
		claim.schedule.ID, claim.schedule.Fence, claim.key.Tenant, claim.key.Account, claim.key.Region, claim.key.Kind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	return err
}
