package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
)

func (s Store) VerifyPulledExport(ctx context.Context, key Key, observation string) (Report, error) {
	if !key.Valid() || !observationDigest.MatchString(observation) {
		return Report{}, ErrInvalid
	}
	var result Report
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		bound, err := accountBinding(ctx, tx, key, false)
		if err != nil {
			return err
		}
		id := pullJobID(key)
		var revision int64
		var reportID string
		err = tx.QueryRowContext(ctx, `SELECT r.revision,b.report_id FROM cmp_billing_pull_batches AS b
			JOIN cmp_billing_reports AS r ON r.id=b.report_id WHERE (b.job_id,b.observation)=($1,$2)`,
			id, observation).Scan(&revision, &reportID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		entry, err := findReport(ctx, tx, key, revision)
		if err != nil || entry.id != reportID {
			if err == nil || errors.Is(err, ErrNotFound) {
				return ErrCorrupt
			}
			return err
		}
		var manifest Manifest
		if json.Unmarshal(entry.manifest, &manifest) != nil || manifest.validate(key.Source) != nil || manifest.Provider != bound.provider {
			return ErrCorrupt
		}
		bill, err := replayPulledObjects(ctx, tx, id, observation)
		if err != nil {
			return err
		}
		prepared, err := prepareReport(ctx, key, manifest, bound.remote, bill.parts)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				return ErrCorrupt
			}
			return err
		}
		if prepared.digest != entry.report.Digest || prepared.rows != entry.report.Rows {
			return ErrCorrupt
		}
		report, err := loadReport(ctx, tx, entry)
		if err != nil {
			return err
		}
		if len(report.Parts) != len(prepared.parts) {
			return ErrCorrupt
		}
		if err := verifyProjection(ctx, tx, reportID, prepared, report.Parts); err != nil {
			return err
		}
		result = report
		return nil
	})
	return result, err
}

func replayPulledObjects(ctx context.Context, tx *sql.Tx, id, observation string) (expandedBill, error) {
	rows, err := tx.QueryContext(ctx, `SELECT object_no,object_key,format,entries,digest,raw FROM cmp_billing_pull_objects
		WHERE (job_id,observation)=($1,$2) ORDER BY object_no`, id, observation)
	if err != nil {
		return expandedBill{}, err
	}
	defer func() { _ = rows.Close() }()
	var bill expandedBill
	count, wireBytes := 0, 0
	for rows.Next() {
		var object pulledObject
		var number int
		var digest string
		var entries []byte
		if err := rows.Scan(&number, &object.Key, &object.Format, &entries, &digest, &object.raw); err != nil {
			return expandedBill{}, err
		}
		count++
		wireBytes += len(object.raw)
		if number != count || wireBytes > MaxTotalBytes || json.Unmarshal(entries, &object.Entries) != nil ||
			!object.valid() || digestOf(object.raw) != digest {
			return expandedBill{}, ErrCorrupt
		}
		if err := bill.appendObject(ctx, object); err != nil {
			if ctx.Err() != nil {
				return expandedBill{}, ctx.Err()
			}
			return expandedBill{}, ErrCorrupt
		}
	}
	if err := rows.Err(); err != nil {
		return expandedBill{}, err
	}
	if count == 0 {
		return expandedBill{}, ErrCorrupt
	}
	return bill, nil
}
