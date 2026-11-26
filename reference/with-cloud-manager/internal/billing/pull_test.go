package billing

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

type pullFixture struct {
	raw   []byte
	etag  string
	err   error
	reads int
}

func (f *pullFixture) Head(_ context.Context, _ string, match string) (provider.ObjectMeta, error) {
	if f.err != nil {
		return provider.ObjectMeta{}, f.err
	}
	if match != "" && match != f.etag {
		return provider.ObjectMeta{}, provider.ErrObjectChanged
	}
	return provider.ObjectMeta{ETag: f.etag, Bytes: int64(len(f.raw))}, nil
}

func (f *pullFixture) Read(_ context.Context, _ string, meta provider.ObjectMeta, _ int64) ([]byte, error) {
	f.reads++
	if meta.ETag != f.etag {
		return nil, provider.ErrObjectChanged
	}
	return f.raw, nil
}

func pullDBFixture(t *testing.T) (Store, PullJob, *pullFixture) {
	t.Helper()
	db := billingDB(t)
	if err := MigratePulls(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	key, manifest := testLedger()
	job := PullJob{Key: key, Generation: 1, IntervalSeconds: 60, Region: "cn-hangzhou", Bucket: "csv-fixture",
		Manifest: manifest, Objects: []PullObject{{Key: "2026-11/filter.csv", Format: pullCSV}}}
	return NewStore(db), job, &pullFixture{raw: partCSV("10002,2026-11,CNY,0.1,0.08"), etag: `"first"`}
}

func requirePuller(t *testing.T, store Store, job PullJob, reader PullReader) Puller {
	t.Helper()
	poller, err := NewPuller(store, job, reader)
	if err != nil {
		t.Fatal(err)
	}
	return poller
}

func preparePullFixture(t *testing.T, puller Puller, claim pullClaim) pulledReport {
	t.Helper()
	observation, objects, err := puller.inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	report, err := puller.download(t.Context(), claim, observation, objects)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestBillingPullLeaseAndAccountGeneration(t *testing.T) {
	store, job, reader := pullDBFixture(t)
	ctx := t.Context()
	first := requirePuller(t, store, job, reader)
	claim, err := first.claim(ctx, false)
	if err != nil || claim.token == "" {
		t.Fatalf("first lease: %+v %v", claim, err)
	}
	report := preparePullFixture(t, first, claim)
	active, err := first.claim(ctx, true)
	if err != nil || active.token != "" {
		t.Fatalf("force stole active lease: %+v %v", active, err)
	}
	conflicting := job
	conflicting.IntervalSeconds++
	if _, err := requirePuller(t, store, conflicting, reader).claim(ctx, true); !errors.Is(err, ErrPullProfile) {
		t.Fatalf("conflicting profile shared a generation: %v", err)
	}
	nextProfile := job
	nextProfile.Generation++
	second := requirePuller(t, store, nextProfile, reader)
	replacement, err := second.claim(ctx, false)
	if err != nil || replacement.token == "" || replacement.token == claim.token {
		t.Fatalf("new generation did not fence old lease: %+v %v", replacement, err)
	}
	if _, err := first.publish(ctx, claim, report); !errors.Is(err, ErrPullLease) {
		t.Fatalf("old profile published: %v", err)
	}
	if _, err := first.claim(ctx, true); !errors.Is(err, ErrPullProfile) {
		t.Fatalf("old generation reclaimed: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE cmp_billing_pull_jobs SET lease_until=clock_timestamp()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.publish(ctx, replacement, report); !errors.Is(err, ErrPullLease) {
		t.Fatalf("expired lease published: %v", err)
	}
	replacement, err = second.claim(ctx, true)
	if err != nil || replacement.token == "" {
		t.Fatalf("expired lease not recovered: %+v %v", replacement, err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.publish(ctx, replacement, report); err == nil {
		t.Fatal("old account binding published")
	}
}

func TestBillingPullRejectsWrongPinnedDigest(t *testing.T) {
	store, job, reader := pullDBFixture(t)
	job.Objects[0].SHA256 = digestOf([]byte("not the expected export"))
	status, err := requirePuller(t, store, job, reader).RunOnce(t.Context(), false)
	if err != nil || status.Outcome != "invalid_export" || status.ReportRevision != nil {
		t.Fatalf("pinned digest mismatch result: %+v %v", status, err)
	}
}

func TestBillingPullRollbackBackoffAndManualReportFence(t *testing.T) {
	store, job, reader := pullDBFixture(t)
	ctx := t.Context()
	poller := requirePuller(t, store, job, reader)
	exec := func(query string) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec("ALTER TABLE cmp_billing_pull_objects ADD CONSTRAINT fixture_reject_archive CHECK (false) NOT VALID")
	status, err := poller.RunOnce(ctx, false)
	if err != nil || status.Outcome != "storage_failed" || status.ReportRevision != nil {
		t.Fatalf("archive failure status %+v: %v", status, err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_billing_reports").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial report leaked: %d %v", count, err)
	}
	exec("ALTER TABLE cmp_billing_pull_objects DROP CONSTRAINT fixture_reject_archive")
	before := reader.reads
	status, err = poller.RunOnce(ctx, false)
	if err != nil || status.Outcome != "storage_failed" || reader.reads != before {
		t.Fatal("scheduled worker ignored backoff")
	}
	reader.err = &provider.Failure{Kind: provider.Throttled, RetryAfter: 5 * time.Minute}
	status, err = poller.RunOnce(ctx, true)
	if err != nil || status.Outcome != "throttled" || status.NextAt.Before(time.Now().Add(4*time.Minute)) {
		t.Fatalf("Retry-After not respected: %+v %v", status, err)
	}
	reader.err = nil
	claim, err := poller.claim(ctx, true)
	if err != nil || claim.token == "" {
		t.Fatalf("no lease: %+v %v", claim, err)
	}
	report := preparePullFixture(t, poller, claim)
	if _, err := store.Import(ctx, job.Key, job.Manifest, 0, [][]byte{partCSV("10002,2026-11,CNY,2,1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := poller.publish(ctx, claim, report); !errors.Is(err, ErrConflict) {
		t.Fatalf("remote pull replaced concurrently imported report: %v", err)
	}
}

func TestBillingPullArchiveRequiresCompleteAndBoundedParts(t *testing.T) {
	var raw bytes.Buffer
	zipWriter := zip.NewWriter(&raw)
	for _, name := range []string{"bill.csv", "bill.csv"} {
		file, err := zipWriter.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte("body")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	var bill expandedBill
	if err := bill.appendObject(t.Context(), pulledObject{PullObject: PullObject{Format: pullZIP, Entries: []string{"bill.csv"}}, raw: raw.Bytes()}); !errors.Is(err, ErrPullArchive) {
		t.Fatalf("duplicate ZIP entries accepted: %v", err)
	}
	raw.Reset()
	gzipWriter := gzip.NewWriter(&raw)
	if _, err := gzipWriter.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	bill.bytes = MaxTotalBytes - 3
	if err := bill.appendObject(t.Context(), pulledObject{PullObject: PullObject{Format: pullGzip}, raw: raw.Bytes()}); !errors.Is(err, provider.ErrObjectSize) {
		t.Fatalf("decompressed bytes exceed cumulative bound: %v", err)
	}
	bill = expandedBill{}
	data := append(raw.Bytes(), 0)
	if err := bill.appendObject(t.Context(), pulledObject{PullObject: PullObject{Format: pullGzip}, raw: data}); !errors.Is(err, ErrPullArchive) {
		t.Fatalf("trailing gzip bytes accepted: %v", err)
	}
}
