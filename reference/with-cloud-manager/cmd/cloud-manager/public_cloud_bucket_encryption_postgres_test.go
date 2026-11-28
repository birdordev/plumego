package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func TestAcceptancePublicCloudBucketEncryptionEnable(t *testing.T) {
	for _, f := range bucketMutationFixtures() {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testBucketEncryptionEnable(t, f)
		})
	}
}

func testBucketEncryptionEnable(t *testing.T, f *bucketFixture) {
	t.Helper()
	const actor = bucketVersionActor
	const reviewer = bucketVersionApprover
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	var writes atomic.Int32
	var loseReceipt atomic.Bool
	f.encryptionMode.Store(2)
	f.putEncryption = func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		const rule = "<ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault>"
		want := "<ServerSideEncryptionConfiguration><Rule>" + rule + "</Rule></ServerSideEncryptionConfiguration>"
		if f.vendor == provider.AlibabaECS {
			want = "<ServerSideEncryptionRule>" + rule + "</ServerSideEncryptionRule>"
		}
		if err != nil || string(body) != want || r.Header.Get("Content-Type") != "application/xml" {
			t.Errorf("invalid native bucket encryption document: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writes.Add(1)
		if loseReceipt.Load() {
			f.encryptionMode.Store(8)
			panic(http.ErrAbortHandler)
		}
		f.encryptionMode.Store(6)
		switch f.vendor {
		case provider.TencentCVM:
			w.Header().Set("X-Cos-Request-Id", "encryption-cos-receipt")
			w.WriteHeader(http.StatusNoContent)
		case provider.AlibabaECS:
			w.Header().Set("X-Oss-Request-Id", "encryption-oss-receipt")
			w.WriteHeader(http.StatusOK)
		case provider.HuaweiECS:
			w.Header().Set("X-Obs-Request-Id", "encryption-obs-receipt")
			w.WriteHeader(http.StatusOK)
		}
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveBuckets(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	profile := writeBucketMutationFixtureProfile(t, f, "fixture/snapshot-delete", false, true)
	id := f.name + "@" + fixtureBucketOld
	run := func(args ...string) {
		t.Helper()
		if err := runPublicCloud(ctx, db, args, f.vendor, profile, transport); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func() { run("refresh-"+f.command+"-bucket-encryption", "tenant", f.vendor, id) }
	run("scan-"+f.command, "tenant", f.vendor, "global", "object-bucket")
	run("refresh-"+f.command+"-bucket-versioning", "tenant", f.vendor, id)
	ops := operations.NewStore(db)
	startBucketMutationFixtureWorker(t, ops, profile, transport)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	input := operations.BucketEncryptionInput{Account: f.vendor, AssetID: id}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	submit := func(project, key, revision, raw string, status int) operations.Operation {
		t.Helper()
		return submitFixtureBucketSetting(t, app, project, "encryption", key, revision, []byte(raw), status)
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := ops.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, ops, scope, actor, op.ID, status, true)
	}
	submit("project", "unobserved", "1", string(body), http.StatusPreconditionFailed)
	refresh()
	submit("other", "wrong-project", "1", string(body), http.StatusForbidden)
	submit("project", "wrong-revision", "2", string(body), http.StatusPreconditionFailed)
	submit("project", "arbitrary-algorithm", "1", strings.TrimSuffix(string(body), "}")+`,"target_state":"kms"}`, http.StatusBadRequest)

	drift := submit("project", "native-kms-drift", "1", string(body), http.StatusAccepted)
	f.encryptionMode.Store(0)
	approve(drift)
	wait(drift, "failed")
	if writes.Load() != 0 {
		t.Fatal("preflight overwrote encryption configured by another writer")
	}
	f.encryptionMode.Store(2)

	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"enable-bucket-encryption", "tenant", "project", "--idempotency-key", "enable", "--if-match", "1"},
		server.URL, actor, bytes.NewReader(body), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil || op.ID == "" || op.Action != "enable-bucket-encryption" {
		t.Fatalf("invalid encryption CLI receipt: %s %v", output.String(), err)
	}
	replay := submit("project", "enable", "1", string(body), http.StatusAccepted)
	if replay.ID != op.ID || op.Status != "awaiting_approval" {
		t.Fatalf("invalid idempotent submission: first=%+v replay=%+v", op, replay)
	}
	if _, err := ops.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approval was accepted: %v", err)
	}
	submitFixtureBucketVersioning(t, app, "project", "competing-versioning", "1", operations.BucketVersioningInput{
		Account: f.vendor, AssetID: id, Current: provider.BucketVersioningEnabled, Target: provider.BucketVersioningSuspended,
	}, http.StatusConflict)
	approve(op)
	done := wait(op, "succeeded")
	if writes.Load() != 1 || done.BucketEncryptionIntent == nil || done.BucketEncryptionIntent.Location != f.location ||
		done.BucketEncryptionIntent.Target != provider.BucketEncryptionAES256 || done.ProviderRequestID == "" {
		t.Fatalf("incomplete enable: %+v writes=%d", done, writes.Load())
	}
	submit("project", "already-configured", "1", string(body), http.StatusPreconditionFailed)

	f.encryptionMode.Store(2)
	refresh()
	loseReceipt.Store(true)
	unknown := submit("project", "lost-receipt", "1", string(body), http.StatusAccepted)
	approve(unknown)
	pending := wait(unknown, "needs_review")
	if writes.Load() != 2 || pending.ProviderRequestID != "" {
		t.Fatalf("unexpected uncertain write: %+v writes=%d", pending, writes.Load())
	}
	if _, err := ops.Resolve(ctx, reviewer, scope, unknown.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/manual"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed remote evidence: %v", err)
	}
	f.encryptionMode.Store(6)
	retryNameChangeObservation(t, ops, scope, reviewer, unknown.ID)
	wait(unknown, "succeeded")
	if writes.Load() != 2 {
		t.Fatalf("observation redelivered the provider write: %d", writes.Load())
	}
	var snapshot inventory.BucketEncryptionSnapshot
	path := "/v1/tenants/tenant/projects/project/assets/object-buckets/" + url.PathEscape(id) + "/encryption?account=" + url.QueryEscape(f.vendor)
	decodeProjectData(t, scheduleResponse(t, app, http.MethodGet, path, actor, "", "", http.StatusOK), &snapshot)
	if snapshot.State != provider.BucketEncryptionAES256 {
		t.Fatalf("readback did not publish the new encryption setting: %+v", snapshot)
	}
	var report inventory.BucketProtectionReport
	reportPath := "/v1/tenants/tenant/projects/project/governance/bucket-protection?account=" + url.QueryEscape(f.vendor)
	decodeProjectData(t, scheduleResponse(t, app, http.MethodGet, reportPath, actor, "", "", http.StatusOK), &report)
	if report.Summary.Encryption.Flagged != 0 || report.Summary.Encryption.Fresh != 1 {
		t.Fatalf("protection report did not reflect confirmed encryption: %+v", report.Summary.Encryption)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox
		WHERE event_type='object-bucket.operation.succeeded'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("unexpected terminal events: count=%d error=%v", count, err)
	}
	f.encryptionMode.Store(2)
	refresh()
	f.recreated.Store(true)
	stale := submit("project", "old-bucket", "1", string(body), http.StatusAccepted)
	approve(stale)
	wait(stale, "failed")
	if writes.Load() != 2 {
		t.Fatal("same-name replacement received a write")
	}
}
