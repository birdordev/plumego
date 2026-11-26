package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
)

func (s Store) BuildAllocation(ctx context.Context, key Key, reportRevision, expected int64, input AllocationPolicy) (Allocation, error) {
	if !key.Valid() || reportRevision < 1 || expected < 0 {
		return Allocation{}, ErrInvalid
	}
	policy, encoded, err := canonicalPolicy(input)
	if err != nil {
		return Allocation{}, err
	}
	digest := policyDigest(encoded)
	var original reportEntry
	var prepared allocationProjection
	var bound binding
	var result Allocation
	err = dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		var err error
		bound, err = accountBinding(ctx, tx, key, false)
		if err != nil {
			return err
		}
		original, err = findReport(ctx, tx, key, reportRevision)
		if err != nil {
			return err
		}
		existing, err := allocationByDigest(ctx, tx, original, digest)
		if err == nil {
			result, err = loadAllocation(ctx, tx, existing)
			return err
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		prepared, err = computeAllocation(ctx, tx, original, policy)
		return err
	})
	if err != nil || result.Replayed {
		return result, err
	}

	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		locked, err := accountBinding(ctx, tx, key, true)
		if err != nil {
			return err
		}
		if locked != bound {
			return ErrAllocationConflict
		}
		report, err := findReport(ctx, tx, key, reportRevision)
		if err != nil {
			return err
		}
		if report.id != original.id || report.report.Digest != original.report.Digest {
			return ErrAllocationConflict
		}
		entry, err := publishAllocation(ctx, tx, report, expected, digest, encoded, prepared)
		if err != nil {
			return err
		}
		result, err = loadAllocation(ctx, tx, entry)
		return err
	})
	return result, err
}

func allocationByDigest(ctx context.Context, tx *sql.Tx, report reportEntry, digest string) (allocationEntry, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM cmp_billing_allocations WHERE report_id=$1 AND policy_digest=$2`,
		report.id, digest).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return allocationEntry{}, ErrNotFound
	}
	if err != nil {
		return allocationEntry{}, err
	}
	entry, err := selectAllocation(ctx, tx, report, revision)
	entry.value.Replayed = err == nil
	return entry, err
}

func publishAllocation(ctx context.Context, tx *sql.Tx, report reportEntry, expected int64, digest string,
	policy []byte, prepared allocationProjection) (allocationEntry, error) {
	entry, err := allocationByDigest(ctx, tx, report, digest)
	if !errors.Is(err, ErrNotFound) {
		return entry, err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(revision),0) FROM cmp_billing_allocations WHERE report_id=$1`,
		report.id).Scan(&revision); err != nil {
		return allocationEntry{}, err
	}
	if revision != expected || expected == 1<<63-1 {
		return allocationEntry{}, ErrAllocationConflict
	}

	var id string
	err = tx.QueryRowContext(ctx, `INSERT INTO cmp_billing_allocations(report_id,revision,algorithm,policy_digest,policy,invalid_tag_rows)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6) RETURNING allocation_id`, report.id, revision+1, allocationAlgorithm,
		digest, policy, prepared.invalidTagRows).Scan(&id)
	if err != nil {
		return allocationEntry{}, err
	}
	totals, err := json.Marshal(prepared.totals)
	if err != nil {
		return allocationEntry{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_allocation_totals
		(allocation_id,currency,center,row_count,billed,effective,list_cost)
		SELECT $1,currency,center,rows,billed_cost,effective_cost,list_cost
		FROM jsonb_to_recordset($2::jsonb) AS line(currency text,center text,rows int,billed_cost numeric,effective_cost numeric,list_cost numeric)`,
		id, totals); err != nil {
		return allocationEntry{}, err
	}
	return selectAllocation(ctx, tx, report, revision+1)
}
