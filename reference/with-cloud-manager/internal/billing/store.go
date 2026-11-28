package billing

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) Store { return Store{db: db} }

func Migrate(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0093_billing_ledger")
}

type binding struct {
	provider, remote string
	revision         int64
}

func accountBinding(ctx context.Context, db identity.Querier, key Key, lock bool) (binding, error) {
	query := `SELECT provider,remote_identity,credential_revision FROM cmp_cloud_accounts
		WHERE tenant_id=$1 AND project_id=$2 AND account_id=$3
		  AND mode IN ('read_only','write_enabled')`
	if lock {
		query += " FOR NO KEY UPDATE"
	}
	var result binding
	err := db.QueryRowContext(ctx, query, key.Tenant, key.Project, key.Account).Scan(&result.provider, &result.remote, &result.revision)
	if errors.Is(err, sql.ErrNoRows) {
		return binding{}, identity.ErrForbidden
	}
	return result, err
}

func (s Store) Get(ctx context.Context, token string, key Key, revision int64) (Report, error) {
	if !key.Valid() || revision < 0 {
		return Report{}, ErrInvalid
	}
	var result Report
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, key); err != nil {
			return err
		}
		if _, err := accountBinding(ctx, tx, key, false); err != nil {
			return err
		}
		entry, err := findReport(ctx, tx, key, revision)
		if err != nil {
			return err
		}
		result, err = loadReport(ctx, tx, entry)
		return err
	})
	return result, err
}

func authorizeBilling(ctx context.Context, tx *sql.Tx, token string, key Key) (identity.Scope, error) {
	auth := identity.NewStore(tx)
	actor, err := auth.AuthenticateRequest(ctx, token)
	if err != nil {
		return identity.Scope{}, err
	}
	if actor.Tenant != key.Tenant {
		return identity.Scope{}, identity.ErrForbidden
	}
	actor.Project = key.Project
	_, err = auth.AuthorizeProject(ctx, actor, identity.Manage)
	return actor, err
}

func readOnly() *sql.TxOptions {
	return &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
}

type reportEntry struct {
	id       string
	manifest []byte
	report   Report
}

func findReport(ctx context.Context, tx *sql.Tx, key Key, revision int64) (reportEntry, error) {
	var entry reportEntry
	report := &entry.report
	report.Account, report.Source, report.Period = key.Account, key.Source, key.Period
	err := tx.QueryRowContext(ctx, `SELECT id,revision,provider,digest,manifest,row_count,imported_at,
		revision=(SELECT max(latest.revision) FROM cmp_billing_reports latest
			WHERE latest.tenant_id=$1 AND latest.project_id=$2 AND latest.account_id=$3 AND latest.source=$4 AND latest.period=$5::date)
		FROM cmp_billing_reports
		WHERE tenant_id=$1 AND project_id=$2 AND account_id=$3 AND source=$4 AND period=$5::date AND ($6::bigint=0 OR revision=$6)
		ORDER BY revision DESC LIMIT 1`, key.Tenant, key.Project, key.Account, key.Source, key.Period+"-01", revision).
		Scan(&entry.id, &report.Revision, &report.Provider, &report.Digest, &entry.manifest, &report.Rows, &report.ImportedAt, &report.Current)
	if errors.Is(err, sql.ErrNoRows) {
		return reportEntry{}, ErrNotFound
	}
	return entry, err
}

func loadReport(ctx context.Context, tx *sql.Tx, entry reportEntry) (Report, error) {
	report := entry.report
	parts, err := readParts(ctx, tx, entry.id)
	if err != nil {
		return Report{}, err
	}
	report.Parts = parts
	rows, err := tx.QueryContext(ctx, `SELECT currency,billed::text,effective::text,list_cost::text,expected_billed::text,
		(billed-expected_billed)::text FROM cmp_billing_totals WHERE report_id=$1 ORDER BY currency`, entry.id)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = rows.Close() }()
	report.Totals = make([]Total, 0)
	for rows.Next() {
		var item Total
		if err := rows.Scan(&item.Currency, &item.Billed, &item.Effective, &item.List, &item.ExpectedBilled, &item.Variance); err != nil {
			return Report{}, err
		}
		report.Totals = append(report.Totals, item)
	}
	return report, rows.Err()
}

func readParts(ctx context.Context, tx *sql.Tx, id string) ([]Part, error) {
	rows, err := tx.QueryContext(ctx, `SELECT part_no,digest,octet_length(raw),row_count
		FROM cmp_billing_parts WHERE report_id=$1 ORDER BY part_no`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	parts := make([]Part, 0)
	for rows.Next() {
		var part Part
		if err := rows.Scan(&part.Number, &part.Digest, &part.Bytes, &part.Rows); err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, rows.Err()
}
