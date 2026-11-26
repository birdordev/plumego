package billing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	dbutil "github.com/birdordev/plumego/store/db"
)

type preparedPart struct {
	digest string
	data   []byte
	lines  []billLine
}

type preparedReport struct {
	digest   string
	manifest []byte
	parts    []preparedPart
	rows     int
}

func digestOf(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func prepareReport(ctx context.Context, key Key, manifest Manifest, remote string, parts [][]byte) (preparedReport, error) {
	if len(parts) == 0 || len(parts) > MaxParts {
		return preparedReport{}, ErrInvalid
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return preparedReport{}, err
	}
	result := preparedReport{manifest: encoded, parts: make([]preparedPart, 0, len(parts))}
	seen := make(map[string]bool, len(parts))
	currencies := make(map[string]bool)
	for currency := range manifest.ExpectedBilled {
		currencies[currency] = true
	}
	bytes := 0
	for index, data := range parts {
		bytes += len(data)
		if len(data) > MaxPartBytes || bytes > MaxTotalBytes {
			return preparedReport{}, fmt.Errorf("%w: billing sources exceed size limit", ErrInvalid)
		}
		digest := digestOf(data)
		if seen[digest] {
			return preparedReport{}, fmt.Errorf("%w: duplicate source file", ErrInvalid)
		}
		seen[digest] = true
		lines, err := parseCSV(ctx, key, manifest, remote, data, maxRows-result.rows)
		if err != nil {
			return preparedReport{}, fmt.Errorf("part %d: %w", index+1, err)
		}
		for _, line := range lines {
			currencies[line.Currency] = true
		}
		result.rows += len(lines)
		result.parts = append(result.parts, preparedPart{digest: digest, data: data, lines: lines})
	}
	if len(currencies) > 64 || len(currencies) == 0 {
		return preparedReport{}, fmt.Errorf("%w: invalid number of billing currencies", ErrInvalid)
	}
	sort.Slice(result.parts, func(i, j int) bool { return result.parts[i].digest < result.parts[j].digest })
	hash := sha256.New()
	_, _ = hash.Write([]byte("cmp-billing-v1\x00"))
	_, _ = hash.Write(encoded)
	_, _ = hash.Write([]byte{0})
	for _, part := range result.parts {
		_, _ = hash.Write([]byte(part.digest))
	}
	result.digest = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func (s Store) Import(ctx context.Context, key Key, manifest Manifest, expected int64, parts [][]byte) (Report, error) {
	if !key.Valid() || expected < 0 {
		return Report{}, ErrInvalid
	}
	if err := manifest.validate(key.Source); err != nil {
		return Report{}, err
	}
	bound, err := accountBinding(ctx, s.db, key, false)
	if err != nil {
		return Report{}, err
	}
	if bound.provider != manifest.Provider {
		return Report{}, fmt.Errorf("%w: provider differs from verified account", ErrInvalid)
	}
	prepared, err := prepareReport(ctx, key, manifest, bound.remote, parts)
	if err != nil {
		return Report{}, err
	}
	var out Report
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		locked, err := accountBinding(ctx, tx, key, true)
		if err != nil {
			return err
		}
		if locked != bound {
			return ErrConflict
		}
		entry, err := publishReport(ctx, tx, key, manifest.Provider, expected, prepared)
		if err != nil {
			return err
		}
		out, err = loadReport(ctx, tx, entry)
		return err
	})
	return out, err
}

func publishReport(ctx context.Context, tx *sql.Tx, key Key, provider string, expected int64, prepared preparedReport) (reportEntry, error) {
	current, err := findReport(ctx, tx, key, 0)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return reportEntry{}, err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM cmp_billing_reports WHERE tenant_id=$1 AND project_id=$2
		AND account_id=$3 AND source=$4 AND period=$5::date AND digest=$6`,
		key.Tenant, key.Project, key.Account, key.Source, key.Period+"-01", prepared.digest).Scan(&revision)
	if err == nil {
		previous, err := findReport(ctx, tx, key, revision)
		previous.report.Replayed = err == nil
		return previous, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return reportEntry{}, err
	}
	if expected != current.report.Revision || expected == 1<<63-1 {
		return reportEntry{}, ErrConflict
	}
	var reportID string
	err = tx.QueryRowContext(ctx, `INSERT INTO cmp_billing_reports
		(tenant_id,project_id,account_id,source,period,revision,provider,digest,manifest,row_count)
		VALUES ($1,$2,$3,$4,$5::date,$6,$7,$8,$9::jsonb,$10) RETURNING id`,
		key.Tenant, key.Project, key.Account, key.Source, key.Period+"-01", expected+1,
		provider, prepared.digest, prepared.manifest, prepared.rows).Scan(&reportID)
	if err != nil {
		return reportEntry{}, err
	}
	if err := storePrepared(ctx, tx, reportID, prepared); err != nil {
		return reportEntry{}, err
	}
	return findReport(ctx, tx, key, expected+1)
}
