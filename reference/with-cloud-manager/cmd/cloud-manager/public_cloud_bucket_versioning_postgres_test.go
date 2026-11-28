package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const bucketVersionActor = "fixture-bucket-versioning-operator"
const bucketVersionApprover = "fixture-bucket-versioning-approver"

func TestAcceptancePublicCloudBucketVersioningChange(t *testing.T) {
	for _, f := range []*bucketFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "tencent", product: provider.TencentCOS, name: "fixture-first-1250000000", location: "ap-beijing"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "alibaba", product: provider.AlibabaOSS, name: "fixture-first-oss", location: "cn-shanghai"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "huawei", product: provider.HuaweiOBS, name: "fixture-first-obs", location: "cn-north-4"},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testBucketVersioningChange(t, f)
		})
	}
}

func testBucketVersioningChange(t *testing.T, f *bucketFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), bucketVersionActor, bucketVersionApprover)
	var writes atomic.Int32
	var loseReceipt atomic.Bool
	f.versionMode.Store(2)
	f.putVersioning = func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			XMLName xml.Name `xml:"VersioningConfiguration"`
			Status  string   `xml:"Status"`
		}
		if err := xml.NewDecoder(r.Body).Decode(&input); err != nil || r.Header.Get("Content-Type") != "application/xml" {
			t.Errorf("invalid bucket versioning document: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch input.Status {
		case "Enabled":
			f.versionMode.Store(0)
		case "Suspended":
			f.versionMode.Store(3)
		default:
			t.Errorf("unsupported native versioning state %q", input.Status)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writes.Add(1)
		if loseReceipt.Load() {
			f.versionMode.Store(4)
			panic(http.ErrAbortHandler)
		}
		switch f.vendor {
		case provider.TencentCVM:
			w.Header().Set("X-Cos-Request-Id", "bucket-fixture-cos")
		case provider.AlibabaECS:
			w.Header().Set("X-Oss-Request-Id", "bucket-fixture-oss")
		case provider.HuaweiECS:
			w.Header().Set("X-Obs-Request-Id", "bucket-fixture-obs")
		}
		w.WriteHeader(http.StatusOK)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveBuckets(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	const ref = "fixture/snapshot-delete"
	profile := writeBucketVersioningFixtureProfile(t, f, ref)
	id := f.name + "@" + fixtureBucketOld
	for _, args := range [][]string{
		{"scan-" + f.command, "tenant", f.vendor, "global", "object-bucket"},
		{"refresh-" + f.command + "-bucket-versioning", "tenant", f.vendor, id},
	} {
		if err := runPublicCloud(ctx, db, args, f.vendor, profile, transport); err != nil {
			t.Fatal(err)
		}
	}

	ops := operations.NewStore(db)
	startVersioningFixtureWorker(t, ops, profile, transport)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := ops.Approve(ctx, bucketVersionApprover, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, ops, scope, bucketVersionActor, op.ID, status, true)
	}
	enable := operations.BucketVersioningInput{Account: f.vendor, AssetID: id, Current: provider.BucketVersioningDisabled, Target: provider.BucketVersioningEnabled}
	submit := func(project, key, revision string, input operations.BucketVersioningInput, expected int) operations.Operation {
		t.Helper()
		return submitFixtureBucketVersioning(t, app, project, key, revision, input, expected)
	}
	submit("other", "wrong-project", "1", enable, http.StatusForbidden)
	submit("project", "stale-revision", "2", enable, http.StatusPreconditionFailed)
	alternate := enable
	alternate.Current = provider.BucketVersioningSuspended
	submit("project", "stale-state", "1", alternate, http.StatusPreconditionFailed)
	alternate.Target = provider.BucketVersioningDisabled
	submit("project", "invalid-target", "1", alternate, http.StatusBadRequest)
	op := submitBucketVersioningCLI(t, server, enable)
	replay := submit("project", "enable", "1", enable, http.StatusAccepted)
	if replay.ID != op.ID || op.Status != "awaiting_approval" || writes.Load() != 0 {
		t.Fatalf("invalid idempotent submission: first=%+v replay=%+v", op, replay)
	}
	if _, err := ops.Approve(ctx, bucketVersionActor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approval was accepted: %v", err)
	}
	submit("project", "competing", "1", enable, http.StatusConflict)
	approve(op)
	done := wait(op, "succeeded")
	if writes.Load() != 1 || done.BucketVersioningIntent == nil || done.BucketVersioningIntent.Location != f.location ||
		done.BucketVersioningIntent.Target != provider.BucketVersioningEnabled || done.ProviderRequestID == "" {
		t.Fatalf("incomplete enable: %+v writes=%d", done, writes.Load())
	}

	suspend := enable
	suspend.Current, suspend.Target = provider.BucketVersioningEnabled, provider.BucketVersioningSuspended
	loseReceipt.Store(true)
	paused := submit("project", "suspend", "1", suspend, http.StatusAccepted)
	approve(paused)
	pending := wait(paused, "needs_review")
	if writes.Load() != 2 || pending.ProviderRequestID != "" {
		t.Fatalf("unknown write did not enter review: %+v writes=%d", pending, writes.Load())
	}
	if _, err := ops.Resolve(ctx, bucketVersionApprover, scope, paused.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/manual"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed remote evidence: %v", err)
	}
	f.versionMode.Store(3)
	retryNameChangeObservation(t, ops, scope, bucketVersionApprover, paused.ID)
	wait(paused, "succeeded")
	if writes.Load() != 2 {
		t.Fatalf("observation retried the provider write: %d", writes.Load())
	}
	var snapshot inventory.BucketVersioningSnapshot
	path := "/v1/tenants/tenant/projects/project/assets/object-buckets/" + url.PathEscape(id) + "/versioning?account=" + url.QueryEscape(f.vendor)
	decodeProjectData(t, scheduleResponse(t, app, http.MethodGet, path, bucketVersionActor, "", "", http.StatusOK), &snapshot)
	if snapshot.State != provider.BucketVersioningSuspended {
		t.Fatalf("observation was not published: %+v", snapshot)
	}
	var outbox int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox
		WHERE event_type='object-bucket.operation.succeeded'`).Scan(&outbox); err != nil || outbox != 2 {
		t.Fatalf("unexpected terminal events: count=%d error=%v", outbox, err)
	}

	f.recreated.Store(true)
	enable.Current = provider.BucketVersioningSuspended
	stale := submit("project", "old-bucket-incarnation", "1", enable, http.StatusAccepted)
	approve(stale)
	wait(stale, "failed")
	if writes.Load() != 2 {
		t.Fatal("same-name replacement received a write")
	}
}

func writeBucketVersioningFixtureProfile(t *testing.T, f *bucketFixture, ref string) string {
	t.Helper()
	config := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, BucketListAccountWide: true,
		AllowBucketVersioningChanges: true}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.Regions = append(config.Regions, f.location)
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	data, err := json.Marshal(publicCloudProfile{Ref: ref, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startVersioningFixtureWorker(t *testing.T, store operations.Store, profile string, transport http.RoundTripper) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() {
		stopped <- store.RunWorker(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return connectPublicCloudOperator(ctx, binding, profile, transport)
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("versioning worker did not stop")
		}
	})
}

func submitFixtureBucketVersioning(t *testing.T, app http.Handler, project, key, revision string, input operations.BucketVersioningInput, expected int) operations.Operation {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/tenants/tenant/projects/"+project+"/object-buckets/versioning", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bucketVersionActor)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("If-Match", `"`+revision+`"`)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != expected {
		t.Fatalf("%s: HTTP %d, expected %d: %s", key, w.Code, expected, w.Body)
	}
	var result operations.Operation
	if expected == http.StatusAccepted {
		decodeProjectData(t, w, &result)
	}
	return result
}

func submitBucketVersioningCLI(t *testing.T, server *httptest.Server, input operations.BucketVersioningInput) operations.Operation {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runOperationsClient(t.Context(), []string{"set-bucket-versioning", "tenant", "project", "--idempotency-key", "enable", "--if-match", "1"},
		server.URL, bucketVersionActor, bytes.NewReader(body), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var result operations.Operation
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.ID == "" || result.Action != "set-bucket-versioning" {
		t.Fatalf("invalid versioning CLI receipt: %s %v", output.String(), err)
	}
	return result
}
