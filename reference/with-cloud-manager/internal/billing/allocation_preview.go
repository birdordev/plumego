package billing

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"strconv"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
)

//go:embed 0166_billing_allocation_publish.sql
var allocationPublishSchema string

const AllocationPublishMigration = "0166_billing_allocation_publish"

func MigrateAllocationPublish(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, AllocationPublishMigration, allocationPublishSchema)
}

type AllocationPreview struct {
	Account         string            `json:"account"`
	Period          string            `json:"period"`
	Source          string            `json:"source"`
	ReportVersion   string            `json:"report_revision_text"`
	ReportCurrent   bool              `json:"report_current"`
	ReportDigest    string            `json:"report_sha256"`
	CurrentVersion  string            `json:"current_revision_text"`
	MatchingVersion string            `json:"matching_revision_text,omitempty"`
	PolicyDigest    string            `json:"policy_sha256"`
	SourceRows      int               `json:"source_rows"`
	InvalidTagRows  int               `json:"invalid_tag_rows"`
	Totals          []AllocationTotal `json:"totals"`
}

func (s Store) PreviewAllocation(ctx context.Context, token string, key Key, reportRevision int64, input AllocationPolicy) (AllocationPreview, error) {
	if !key.Valid() || reportRevision < 1 {
		return AllocationPreview{}, ErrInvalid
	}
	policy, encoded, err := canonicalPolicy(input)
	if err != nil {
		return AllocationPreview{}, err
	}
	digest := policyDigest(encoded)
	var result AllocationPreview
	err = dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
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
		revision, err := latestAllocationRevision(ctx, tx, report.id)
		if err != nil {
			return err
		}
		result = AllocationPreview{Account: key.Account, Period: key.Period, Source: key.Source,
			ReportVersion: strconv.FormatInt(reportRevision, 10), ReportCurrent: report.report.Current,
			ReportDigest: report.report.Digest, SourceRows: report.report.Rows,
			PolicyDigest: digest, CurrentVersion: strconv.FormatInt(revision, 10)}
		previous, err := allocationByDigest(ctx, tx, report, digest)
		if err == nil {
			value, err := loadAllocation(ctx, tx, previous)
			if err != nil {
				return err
			}
			result.MatchingVersion = value.Version
			result.InvalidTagRows, result.Totals = value.InvalidTagRows, value.Totals
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		projection, err := computeAllocation(ctx, tx, report, policy)
		if err != nil {
			return err
		}
		result.InvalidTagRows, result.Totals = projection.invalidTagRows, projection.totals
		return nil
	})
	return result, err
}
