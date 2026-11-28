package billing

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
)

//go:embed 0095_billing_allocations.sql
var allocationSchema string

const MaxAllocationPolicyBytes = 64 << 10

var ErrAllocationConflict = errors.New("billing allocation revision changed")

type AllocationTotal struct {
	Currency        string  `json:"currency"`
	Center          string  `json:"center"`
	Rows            int     `json:"rows"`
	Billed          string  `json:"billed_cost"`
	Effective       *string `json:"effective_cost,omitempty"`
	List            *string `json:"list_cost,omitempty"`
	ListMinusBilled *string `json:"list_minus_billed,omitempty"`
}

type Allocation struct {
	Account        string            `json:"account"`
	Period         string            `json:"period"`
	Source         string            `json:"source"`
	ReportRevision int64             `json:"report_revision"`
	ReportVersion  string            `json:"report_revision_text"`
	ReportDigest   string            `json:"report_sha256"`
	ReportCurrent  bool              `json:"report_current"`
	Revision       int64             `json:"revision"`
	Version        string            `json:"revision_text"`
	Algorithm      string            `json:"algorithm"`
	PolicyDigest   string            `json:"policy_sha256"`
	CreatedAt      time.Time         `json:"created_at"`
	SourceRows     int               `json:"source_rows"`
	InvalidTagRows int               `json:"invalid_tag_rows"`
	Current        bool              `json:"current"`
	Replayed       bool              `json:"replayed,omitempty"`
	Totals         []AllocationTotal `json:"totals"`
}

type AllocationReplay struct {
	Allocation
	Policy AllocationPolicy `json:"policy"`
}

type allocationEntry struct {
	id     string
	policy []byte
	value  Allocation
}

func MigrateAllocations(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0095_billing_allocations", allocationSchema)
}

func selectAllocation(ctx context.Context, tx *sql.Tx, report reportEntry, revision int64) (allocationEntry, error) {
	value := report.report
	entry := allocationEntry{value: Allocation{Account: value.Account, Period: value.Period, Source: value.Source,
		ReportRevision: value.Revision, ReportDigest: value.Digest, ReportCurrent: value.Current, SourceRows: value.Rows}}
	output := &entry.value
	err := tx.QueryRowContext(ctx, `SELECT allocation_id,revision,algorithm,policy_digest,policy,invalid_tag_rows,created_at,
		revision=(SELECT max(latest.revision) FROM cmp_billing_allocations AS latest WHERE latest.report_id=$1)
		FROM cmp_billing_allocations
		WHERE report_id=$1 AND ($2::bigint=0 OR revision=$2) ORDER BY revision DESC LIMIT 1`, report.id, revision).
		Scan(&entry.id, &output.Revision, &output.Algorithm, &output.PolicyDigest, &entry.policy,
			&output.InvalidTagRows, &output.CreatedAt, &output.Current)
	if errors.Is(err, sql.ErrNoRows) {
		return allocationEntry{}, ErrNotFound
	}
	if err == nil {
		output.Version = strconv.FormatInt(output.Revision, 10)
		output.ReportVersion = strconv.FormatInt(output.ReportRevision, 10)
	}
	return entry, err
}

func loadAllocation(ctx context.Context, tx *sql.Tx, entry allocationEntry) (Allocation, error) {
	value := entry.value
	rows, err := tx.QueryContext(ctx, `SELECT currency,center,row_count,billed::text,effective::text,list_cost::text,(list_cost-billed)::text
		FROM cmp_billing_allocation_totals WHERE allocation_id=$1 ORDER BY currency COLLATE "C",center COLLATE "C"`, entry.id)
	if err != nil {
		return Allocation{}, err
	}
	defer func() { _ = rows.Close() }()
	value.Totals = make([]AllocationTotal, 0)
	for rows.Next() {
		var total AllocationTotal
		if err := rows.Scan(&total.Currency, &total.Center, &total.Rows, &total.Billed, &total.Effective, &total.List, &total.ListMinusBilled); err != nil {
			return Allocation{}, err
		}
		value.Totals = append(value.Totals, total)
	}
	return value, rows.Err()
}

func (s Store) ReadAllocation(ctx context.Context, token string, key Key, reportRevision, revision int64) (Allocation, error) {
	if !key.Valid() || reportRevision < 0 || revision < 0 || (revision > 0 && reportRevision == 0) {
		return Allocation{}, ErrInvalid
	}
	var result Allocation
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, key); err != nil {
			return err
		}
		if _, err := accountBinding(ctx, tx, key, false); err != nil {
			return err
		}
		report, err := findReport(ctx, tx, key, reportRevision)
		if err != nil {
			return err
		}
		entry, err := selectAllocation(ctx, tx, report, revision)
		if err != nil {
			return err
		}
		result, err = loadAllocation(ctx, tx, entry)
		return err
	})
	return result, err
}

func (s Store) VerifyAllocation(ctx context.Context, key Key, reportRevision, revision int64) (AllocationReplay, error) {
	if !key.Valid() || reportRevision < 1 || revision < 1 {
		return AllocationReplay{}, ErrInvalid
	}
	var result AllocationReplay
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := accountBinding(ctx, tx, key, false); err != nil {
			return err
		}
		report, err := findReport(ctx, tx, key, reportRevision)
		if err != nil {
			return err
		}
		entry, err := selectAllocation(ctx, tx, report, revision)
		if err != nil {
			return err
		}
		var policy AllocationPolicy
		if entry.value.Algorithm != allocationAlgorithm || json.Unmarshal(entry.policy, &policy) != nil {
			return ErrCorrupt
		}
		canonical, encoded, err := canonicalPolicy(policy)
		if err != nil || policyDigest(encoded) != entry.value.PolicyDigest {
			return ErrCorrupt
		}
		projection, err := computeAllocation(ctx, tx, report, canonical)
		if err != nil {
			return err
		}
		loaded, err := loadAllocation(ctx, tx, entry)
		if err != nil {
			return err
		}
		if loaded.InvalidTagRows != projection.invalidTagRows || !reflect.DeepEqual(loaded.Totals, projection.totals) {
			return ErrCorrupt
		}
		result = AllocationReplay{Allocation: loaded, Policy: canonical}
		return nil
	})
	return result, err
}
