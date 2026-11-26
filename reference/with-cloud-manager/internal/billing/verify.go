package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
)

func (s Store) Source(ctx context.Context, key Key, revision int64, number int) ([]byte, error) {
	if !key.Valid() || revision < 1 || number < 1 || number > MaxParts {
		return nil, ErrInvalid
	}
	var data []byte
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := accountBinding(ctx, tx, key, false); err != nil {
			return err
		}
		entry, err := findReport(ctx, tx, key, revision)
		if err != nil {
			return err
		}
		var digest string
		if err := tx.QueryRowContext(ctx, `SELECT digest,raw FROM cmp_billing_parts
			WHERE report_id=$1 AND part_no=$2`, entry.id, number).Scan(&digest, &data); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if digestOf(data) != digest {
			return ErrCorrupt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s Store) Verify(ctx context.Context, key Key, revision int64) (Report, error) {
	if !key.Valid() || revision < 1 {
		return Report{}, ErrInvalid
	}
	var result Report
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		bound, err := accountBinding(ctx, tx, key, false)
		if err != nil {
			return err
		}
		entry, err := findReport(ctx, tx, key, revision)
		if err != nil {
			return err
		}
		report, err := loadReport(ctx, tx, entry)
		if err != nil {
			return err
		}
		var manifest Manifest
		if err := json.Unmarshal(entry.manifest, &manifest); err != nil || manifest.validate(key.Source) != nil ||
			manifest.Provider != bound.provider || report.Provider != bound.provider {
			return ErrCorrupt
		}
		raw, err := readRawParts(ctx, tx, entry.id, report.Parts)
		if err != nil {
			return err
		}
		prepared, err := prepareReport(ctx, key, manifest, bound.remote, raw)
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				return ErrCorrupt
			}
			return err
		}
		if prepared.digest != report.Digest || prepared.rows != report.Rows {
			return ErrCorrupt
		}
		if err := verifyProjection(ctx, tx, entry.id, prepared, report.Parts); err != nil {
			return err
		}
		result = report
		return nil
	})
	return result, err
}

func readRawParts(ctx context.Context, tx *sql.Tx, id string, parts []Part) ([][]byte, error) {
	if len(parts) == 0 || len(parts) > MaxParts {
		return nil, ErrCorrupt
	}
	size := 0
	for i, part := range parts {
		if part.Number != i+1 || part.Bytes < 1 || part.Bytes > MaxPartBytes {
			return nil, ErrCorrupt
		}
		size += part.Bytes
	}
	if size > MaxTotalBytes {
		return nil, ErrCorrupt
	}
	raw := make([][]byte, len(parts))
	for i, part := range parts {
		if err := tx.QueryRowContext(ctx, `SELECT raw FROM cmp_billing_parts WHERE report_id=$1 AND part_no=$2`,
			id, part.Number).Scan(&raw[i]); err != nil {
			return nil, err
		}
		if len(raw[i]) != part.Bytes || digestOf(raw[i]) != part.Digest {
			return nil, ErrCorrupt
		}
	}
	return raw, nil
}

const linesVerificationSQL = `WITH stored AS (
	SELECT row_no,currency,billed,effective,list_cost,resource_id,service,region,category,tags
	FROM cmp_billing_lines WHERE report_id=$1 AND part_no=$2
), replay AS (` + recordsetSQL + `)
SELECT NOT EXISTS (
	(SELECT * FROM stored EXCEPT ALL SELECT * FROM replay)
	UNION ALL
	(SELECT * FROM replay EXCEPT ALL SELECT * FROM stored)
)`

const totalsVerificationSQL = `WITH stored AS (
	SELECT currency,billed,effective,list_cost,expected_billed FROM cmp_billing_totals WHERE report_id=$1
), replay AS (` + totalProjectionSQL + `)
SELECT NOT EXISTS (
	(SELECT * FROM stored EXCEPT ALL SELECT * FROM replay)
	UNION ALL
	(SELECT * FROM replay EXCEPT ALL SELECT * FROM stored)
)`

func verifyProjection(ctx context.Context, tx *sql.Tx, id string, prepared preparedReport, parts []Part) error {
	for index, part := range prepared.parts {
		if len(part.lines) != parts[index].Rows || part.digest != parts[index].Digest {
			return ErrCorrupt
		}
		data, err := json.Marshal(part.lines)
		if err != nil {
			return err
		}
		var matches bool
		if err := tx.QueryRowContext(ctx, linesVerificationSQL, id, index+1, data).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return ErrCorrupt
		}
	}
	var matches bool
	if err := tx.QueryRowContext(ctx, totalsVerificationSQL, id, prepared.manifest).Scan(&matches); err != nil {
		return err
	}
	if !matches {
		return ErrCorrupt
	}
	return nil
}
