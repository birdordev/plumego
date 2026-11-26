package billing

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

var observationDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

const pullStatusFields = `outcome,generation,COALESCE(observation,''),report_revision,next_at,attempted_at,failures`

type pullClaim struct {
	token    string
	expected int64
	bound    binding
	status   PullStatus
}

func scanPullStatus(scanner interface{ Scan(...any) error }, status *PullStatus, extra ...any) error {
	return scanner.Scan(append([]any{&status.Outcome, &status.Generation, &status.Observation, &status.ReportRevision,
		&status.NextAt, &status.AttemptedAt, &status.Failures}, extra...)...)
}

func (p Puller) claim(ctx context.Context, force bool) (pullClaim, error) {
	var claim pullClaim
	err := dbutil.WithTransaction(ctx, p.store.db, nil, func(tx *sql.Tx) error {
		job := p.job
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_pull_jobs
			(job_id,tenant_id,project_id,account_id,source,period,generation,profile_digest)
			SELECT $1,$2,$3,$4,$5,$6::date,$7,$8
			FROM cmp_cloud_accounts WHERE (tenant_id,project_id,account_id,provider)=($2,$3,$4,$9)
			ON CONFLICT (job_id) DO NOTHING`,
			p.id, job.Tenant, job.Project, job.Account, job.Source, job.Period+"-01", job.Generation, p.digest, job.Manifest.Provider); err != nil {
			return err
		}
		var profile string
		var leased, due bool
		err := scanPullStatus(tx.QueryRowContext(ctx, "SELECT "+pullStatusFields+`,profile_digest,
			COALESCE(lease_until>clock_timestamp(),false),next_at<=clock_timestamp()
			FROM cmp_billing_pull_jobs WHERE job_id=$1 FOR UPDATE`, p.id), &claim.status, &profile, &leased, &due)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		if claim.status.Generation > job.Generation || claim.status.Generation == job.Generation && profile != p.digest {
			return ErrPullProfile
		}
		if claim.status.Generation < job.Generation {
			err := scanPullStatus(tx.QueryRowContext(ctx, `UPDATE cmp_billing_pull_jobs SET generation=$2,profile_digest=$3,
				lease_token=NULL,lease_until=NULL,next_at=clock_timestamp(),attempted_at=NULL,failures=0,
				outcome='pending',observation=NULL,report_revision=NULL WHERE job_id=$1 RETURNING `+pullStatusFields,
				p.id, job.Generation, p.digest), &claim.status)
			if err != nil {
				return err
			}
			leased, due = false, true
		}
		if leased || !due && !force {
			return nil
		}
		bound, err := accountBinding(ctx, tx, job.Key, false)
		if errors.Is(err, identity.ErrForbidden) || err == nil && bound.provider != job.Manifest.Provider {
			return scanPullStatus(tx.QueryRowContext(ctx, `UPDATE cmp_billing_pull_jobs SET outcome='account_unavailable',
				next_at=clock_timestamp()+make_interval(secs=>$2), attempted_at=clock_timestamp(),
				lease_token=NULL,lease_until=NULL WHERE job_id=$1 RETURNING `+pullStatusFields, p.id, job.IntervalSeconds), &claim.status)
		}
		if err != nil {
			return err
		}
		claim.bound = bound
		current, err := findReport(ctx, tx, job.Key, 0)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		claim.expected = current.report.Revision
		return tx.QueryRowContext(ctx, `UPDATE cmp_billing_pull_jobs SET lease_token=gen_random_uuid(),
			lease_until=clock_timestamp()+make_interval(secs=>$2),attempted_at=clock_timestamp(),outcome='pulling'
			WHERE job_id=$1 RETURNING lease_token`, p.id, int(pullLease.Seconds())).Scan(&claim.token)
	})
	return claim, err
}

func (p Puller) requireClaim(ctx context.Context, tx *sql.Tx, claim pullClaim) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT lease_until>clock_timestamp() FROM cmp_billing_pull_jobs
		WHERE (job_id,generation,lease_token)=($1,$2,$3::uuid) FOR UPDATE`, p.id, p.job.Generation, claim.token).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !valid {
		return ErrPullLease
	}
	return err
}

func (p Puller) finishPull(ctx context.Context, tx *sql.Tx, claim pullClaim, outcome string, delay, failures int, observation string, revision *int64) (PullStatus, error) {
	var status PullStatus
	err := scanPullStatus(tx.QueryRowContext(ctx, `UPDATE cmp_billing_pull_jobs SET
		next_at=clock_timestamp()+make_interval(secs=>$4),lease_token=NULL,lease_until=NULL,outcome=$5,failures=$6,
		observation=NULLIF($7,''),report_revision=$8
		WHERE (job_id,generation,lease_token)=($1,$2,$3::uuid) AND lease_until>clock_timestamp()
		RETURNING `+pullStatusFields, p.id, p.job.Generation, claim.token, delay, outcome, failures, observation, revision), &status)
	if errors.Is(err, sql.ErrNoRows) {
		return PullStatus{}, ErrPullLease
	}
	return status, err
}

func (s Store) PullStatus(ctx context.Context, key Key) (PullStatus, error) {
	if !key.Valid() {
		return PullStatus{}, ErrInvalid
	}
	var status PullStatus
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := accountBinding(ctx, tx, key, false); err != nil {
			return err
		}
		err := scanPullStatus(tx.QueryRowContext(ctx, "SELECT "+pullStatusFields+` FROM cmp_billing_pull_jobs WHERE job_id=$1`,
			pullJobID(key)), &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return status, err
}

func (s Store) PulledObject(ctx context.Context, key Key, observation string, number int) ([]byte, error) {
	if !key.Valid() || !observationDigest.MatchString(observation) || number < 1 || number > MaxParts {
		return nil, ErrInvalid
	}
	var raw []byte
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := accountBinding(ctx, tx, key, false); err != nil {
			return err
		}
		var digest string
		err := tx.QueryRowContext(ctx, `SELECT raw,digest FROM cmp_billing_pull_objects
			WHERE (job_id,observation,object_no)=($1,$2,$3)`, pullJobID(key), observation, number).Scan(&raw, &digest)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if digestOf(raw) != digest {
			return ErrCorrupt
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return raw, nil
}
