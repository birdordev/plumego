package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type pulledReport struct {
	observation string
	objects     []pulledObject
	prepared    preparedReport
}

func (p Puller) RunOnce(ctx context.Context, force bool) (PullStatus, error) {
	claim, err := p.claim(ctx, force)
	if err != nil || claim.token == "" {
		return claim.status, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	observation, objects, err := p.inspect(fetchCtx)
	if err == nil && observation == claim.status.Observation {
		return p.finishWithoutReport(ctx, claim, "unchanged", nil)
	}
	if err == nil {
		var report pulledReport
		report, err = p.download(fetchCtx, claim, observation, objects)
		if err == nil {
			status, publishErr := p.publish(fetchCtx, claim, report)
			if publishErr == nil {
				return status, nil
			}
			if errors.Is(publishErr, ErrPullLease) || errors.Is(publishErr, ErrPullProfile) {
				return PullStatus{}, publishErr
			}
			err = publishErr
		}
	}
	if ctx.Err() != nil {
		return PullStatus{}, ctx.Err()
	}
	return p.finishWithoutReport(ctx, claim, pullFailure(err), err)
}

func (p Puller) inspect(ctx context.Context) (string, []pulledObject, error) {
	objects := make([]pulledObject, 0, len(p.job.Objects))
	var size int64
	for _, object := range p.job.Objects {
		meta, err := p.reader.Head(ctx, object.Key, "")
		if err != nil {
			return "", nil, err
		}
		if meta.Bytes < 1 || meta.Bytes > MaxTotalBytes-size || (object.Format == pullCSV && meta.Bytes > MaxPartBytes) {
			return "", nil, provider.ErrObjectSize
		}
		size += meta.Bytes
		objects = append(objects, pulledObject{PullObject: object, ObjectMeta: meta})
	}
	data, err := json.Marshal(objects)
	if err != nil {
		return "", nil, err
	}
	return digestOf(append([]byte(p.digest+"\x00"), data...)), objects, nil
}

func (p Puller) download(ctx context.Context, claim pullClaim, observation string, objects []pulledObject) (pulledReport, error) {
	var bill expandedBill
	for i := range objects {
		object := &objects[i]
		raw, err := p.reader.Read(ctx, object.Key, object.ObjectMeta, MaxTotalBytes)
		if err != nil {
			return pulledReport{}, err
		}
		if int64(len(raw)) != object.Bytes {
			return pulledReport{}, provider.ErrObjectChanged
		}
		if object.SHA256 != "" && digestOf(raw) != object.SHA256 {
			return pulledReport{}, ErrPullArchive
		}
		object.raw = raw
		if err := bill.appendObject(ctx, *object); err != nil {
			return pulledReport{}, err
		}
	}
	for _, object := range objects {
		meta, err := p.reader.Head(ctx, object.Key, object.ETag)
		if err != nil {
			return pulledReport{}, err
		}
		if meta != object.ObjectMeta {
			return pulledReport{}, provider.ErrObjectChanged
		}
	}
	prepared, err := prepareReport(ctx, p.job.Key, p.job.Manifest, claim.bound.remote, bill.parts)
	if err != nil {
		return pulledReport{}, err
	}
	if prepared.rows == 0 {
		return pulledReport{}, ErrPullArchive
	}
	return pulledReport{observation: observation, prepared: prepared, objects: objects}, nil
}

func (p Puller) publish(ctx context.Context, claim pullClaim, report pulledReport) (PullStatus, error) {
	var status PullStatus
	err := dbutil.WithTransaction(ctx, p.store.db, nil, func(tx *sql.Tx) error {
		if err := p.requireClaim(ctx, tx, claim); err != nil {
			return err
		}
		bound, err := accountBinding(ctx, tx, p.job.Key, true)
		if err != nil {
			return err
		}
		if bound != claim.bound {
			return identity.ErrForbidden
		}
		entry, err := publishReport(ctx, tx, p.job.Key, bound.provider, claim.expected, report.prepared)
		if err != nil {
			return err
		}
		if err := p.archive(ctx, tx, entry.id, report); err != nil {
			return err
		}
		outcome := "published"
		if entry.report.Replayed {
			outcome = "replayed"
		}
		if !entry.report.Current {
			outcome = "historical"
		}
		status, err = p.finishPull(ctx, tx, claim, outcome, p.job.IntervalSeconds, 0, report.observation, &entry.report.Revision)
		return err
	})
	return status, err
}

func (p Puller) archive(ctx context.Context, tx *sql.Tx, reportID string, report pulledReport) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_pull_batches(job_id,observation,report_id,generation,region,bucket)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (job_id,observation) DO NOTHING`,
		p.id, report.observation, reportID, p.job.Generation, p.job.Region, p.job.Bucket)
	if err != nil {
		return err
	}
	added, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if added == 0 {
		var same bool
		if err := tx.QueryRowContext(ctx, `SELECT report_id=$3::uuid FROM cmp_billing_pull_batches
			WHERE (job_id,observation)=($1,$2)`, p.id, report.observation, reportID).Scan(&same); err != nil {
			return err
		}
		if !same {
			return ErrCorrupt
		}
		return nil
	}
	for i, object := range report.objects {
		entries, err := json.Marshal(object.Entries)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_pull_objects
			(job_id,observation,object_no,object_key,format,etag,entries,digest,raw)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)`, p.id, report.observation, i+1, object.Key, object.Format,
			object.ETag, entries, digestOf(object.raw), object.raw); err != nil {
			return err
		}
	}
	return nil
}

func (p Puller) finishWithoutReport(ctx context.Context, claim pullClaim, outcome string, cause error) (PullStatus, error) {
	delay, failures := p.job.IntervalSeconds, 0
	if cause != nil {
		failures = min(claim.status.Failures, 30) + 1
		delay = max(delay, min(3600, 30*(1<<min(failures, 7))))
		var failure *provider.Failure
		if errors.As(cause, &failure) && failure.RetryAfter > 0 {
			retry := min(failure.RetryAfter, MaxPullInterval*time.Second)
			delay = max(delay, int((retry+time.Second-1)/time.Second))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var status PullStatus
	err := dbutil.WithTransaction(ctx, p.store.db, nil, func(tx *sql.Tx) error {
		var err error
		status, err = p.finishPull(ctx, tx, claim, outcome, delay, failures, claim.status.Observation, claim.status.ReportRevision)
		return err
	})
	return status, err
}

func pullFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out"
	case errors.Is(err, provider.ErrObjectChanged):
		return "object_changed"
	case errors.Is(err, provider.ErrObjectSize), errors.Is(err, ErrPullArchive), errors.Is(err, ErrInvalid):
		return "invalid_export"
	case errors.Is(err, ErrConflict):
		return "report_changed"
	case errors.Is(err, identity.ErrForbidden):
		return "account_unavailable"
	}
	var failure *provider.Failure
	if errors.As(err, &failure) {
		switch failure.Kind {
		case provider.Throttled:
			return "throttled"
		case provider.Unauthorized:
			return "denied"
		case provider.Absent:
			return "not_found"
		case provider.InvalidResponse:
			return "invalid_export"
		case provider.Transient:
			return "transport_failed"
		}
	}
	return "storage_failed"
}
