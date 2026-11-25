package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func reserveTenantIngress(ctx context.Context, tx *sql.Tx, tenant string, regular, high int) error {
	if regular+high == 0 {
		return nil
	}
	var regularLimit, urgentLimit, regularUsed, urgentUsed int
	var started sql.NullTime
	var actor sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT regular_per_second,urgent_per_second,regular_used,urgent_used,
		bucket_started_at,urgent_actor FROM scheduler_tenant_ingress WHERE tenant_id=$1 FOR UPDATE`, tenant).
		Scan(&regularLimit, &urgentLimit, &regularUsed, &urgentUsed, &started, &actor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT date_trunc('second',clock_timestamp())").Scan(&now); err != nil {
		return err
	}
	if !started.Valid || now.After(started.Time) {
		regularUsed, urgentUsed = 0, 0
	} else {
		now = started.Time
	}
	identity, _ := ctx.Value(auditRequestKey{}).(auditIdentity)
	if urgentLimit > 0 && actor.Valid && actor.String == identity.actor {
		urgentUsed += high
	} else {
		regular += high
		high = 0
	}
	regularUsed += regular
	if (regular > 0 && regularUsed > regularLimit) || (high > 0 && urgentUsed > urgentLimit) {
		return errTenantCapacity
	}
	_, err = tx.ExecContext(ctx, `UPDATE scheduler_tenant_ingress
		SET bucket_started_at=$2,regular_used=$3,urgent_used=$4 WHERE tenant_id=$1`,
		tenant, now, regularUsed, urgentUsed)
	return err
}
