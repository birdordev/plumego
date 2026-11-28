package delivery

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/migrations"
)

func (r *Reconciliation) reconcileMissing(ctx context.Context, tx *sql.Tx, dest Destination, input []byte) (err error) {
	rows, err := tx.QueryContext(ctx, migrations.ReconcileCoverage(), dest.tenant, dest.project, dest.id,
		r.Coverage.From, r.Coverage.Until, input, MaxReconcileReceipts+1)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	const state = "receiver_missing"
	for rows.Next() {
		if r.Counts[state] == MaxReconcileReceipts {
			return ErrReconcileWindowTooLarge
		}
		var recordedAt time.Time
		item := ReconciliationItem{State: state}
		if err := rows.Scan(&item.OperationID, &item.SourceStatus, &recordedAt, &item.SourceDelivery,
			&item.SourceDeliveryState); err != nil {
			return err
		}
		recordedAt = recordedAt.UTC()
		item.SourceOccurredAt = &recordedAt
		item.EffectID = "cmp-op-final-v1:" + item.OperationID
		r.Items = append(r.Items, item)
		r.Counts[state]++
		r.Clean = false
	}
	return rows.Err()
}
