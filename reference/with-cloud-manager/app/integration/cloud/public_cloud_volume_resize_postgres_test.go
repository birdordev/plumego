package cloudtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const volumeResizeActor = "fixture-volume-resize-executor-token"
const volumeResizeReviewer = "fixture-volume-resize-reviewer-token"

func TestAcceptancePublicCloudVolumeResize(t *testing.T) {
	for _, target := range []struct{ vendor, account, region, disk, snapshot, state, pending string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "disk-fixture", "snap-fixture", "UNATTACHED", "EXPANDING"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "d-fixture", "s-fixture", "Available", "Expanding"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "30000000-0000-4000-8000-000000000010", "40000000-0000-4000-8000-000000000010", "available", "extending"},
	} {
		t.Run(target.vendor, func(t *testing.T) {
			t.Parallel()
			f := &volumeResizeFixture{vpcCreationFixture: vpcCreationFixture{vendor: target.vendor, account: target.account, region: target.region},
				id: target.disk, snapshot: target.snapshot, available: target.state, resizing: target.pending}
			testPublicCloudVolumeResize(t, f)
		})
	}
}

func testPublicCloudVolumeResize(t *testing.T, f *volumeResizeFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	snapshotClass := "standard"
	if f.vendor == provider.TencentCVM {
		snapshotClass = "PRIVATE_SNAPSHOT"
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region, volume: f.id, id: f.snapshot, class: snapshotClass},
		f.remoteIdentity(), volumeResizeActor, volumeResizeReviewer)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_inventory_objects SET status=$2,removed_at=NULL,
		volume_details='{"size_gib":60,"storage_class":"fixture-class","usage":"data"}'::jsonb WHERE external_id=$1`, f.id, f.available); err != nil {
		t.Fatal(err)
	}
	f.size.Store(60)
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	store, assets := operations.NewStore(db), inventory.NewStore(db)
	var enabled atomic.Bool
	startVolumeResizeWorker(t, store, f, &enabled)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	payload := func(current, target int) []byte {
		t.Helper()
		body, err := json.Marshal(operations.VolumeResizeInput{
			Account: f.vendor, Region: f.region, AssetID: f.id, CurrentGiB: current, TargetGiB: target,
		})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	const base = "/v1/tenants/tenant/projects/"
	submit := func(project, key, version string, body []byte, code int) operations.Operation {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, base+project+"/volumes/resize", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+volumeResizeActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", `"`+version+`"`)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != code {
			t.Fatalf("%s: status=%d want=%d body=%s", key, w.Code, code, w.Body.String())
		}
		var op operations.Operation
		if code == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.ResourceKind != provider.Volume || op.Action != "resize-volume" ||
				w.Header().Get("Location") != base+project+"/operations/"+op.ID {
				t.Fatalf("wrong receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, volumeResizeReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, volumeResizeActor, op.ID, state, true)
	}

	body := payload(60, 120)
	submit("other", "wrong-project", "1", body, http.StatusForbidden)
	submit("project", "invalid-capacity", "1", payload(60, 59), http.StatusBadRequest)
	submit("project", "old-capacity", "1", payload(59, 120), http.StatusPreconditionFailed)
	submit("project", "old-ownership", "2", body, http.StatusPreconditionFailed)
	disabled := submit("project", "disabled", "1", body, http.StatusAccepted)
	approve(disabled)
	if op := wait(disabled, "failed"); op.OutcomeReason != "volume_resize_disabled" {
		t.Fatalf("disabled volume resize: %+v", op)
	}
	enabled.Store(true)
	f.prepaid.Store(true)
	prepaid := submit("project", "prepaid", "1", body, http.StatusAccepted)
	approve(prepaid)
	if op := wait(prepaid, "failed"); op.OutcomeReason != "volume_resize_preconditions_unconfirmed" || f.sendCount.Load() != 0 {
		t.Fatalf("prepaid disk could be resized: %+v", op)
	}
	f.prepaid.Store(false)

	var output bytes.Buffer
	if err := commands.RunOperationsClient(ctx, []string{"resize-volume", "tenant", "project", "--idempotency-key", "first", "--if-match", "1"},
		local.URL, volumeResizeActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var first operations.Operation
	if err := json.Unmarshal(output.Bytes(), &first); err != nil || first.Status != "awaiting_approval" || first.ID == "" {
		t.Fatalf("wrong CLI receipt: %s %v", output.String(), err)
	}
	if _, err := store.Approve(ctx, volumeResizeActor, scope, first.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester approved own resize: %v", err)
	}
	if replay := submit("project", "first", "1", body, http.StatusAccepted); replay.ID != first.ID || f.sendCount.Load() != 0 {
		t.Fatalf("write before independent approval or wrong replay: %+v", replay)
	}
	submit("project", "first", "1", payload(60, 121), http.StatusConflict)
	submit("project", "second-active", "1", body, http.StatusConflict)
	snapshot := operations.SnapshotDeletion{Account: f.vendor, Region: f.region, AssetID: f.snapshot, VolumeID: f.id}
	if _, err := store.SubmitSnapshotDeletion(ctx, volumeResizeActor, scope, "snapshot-conflict", 1, snapshot); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("snapshot mutation could enter during resize: %v", err)
	}
	scan, err := assets.Claim(ctx, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.Volume}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.holdAfterWrite.Store(true)
	approve(first)
	if op := wait(first, "observing"); op.ProviderRequestID != volumeResizeReceipt || f.sendCount.Load() != 1 {
		t.Fatalf("wrong first dispatch: %+v", op)
	}
	waitPendingCloudObservation(t, db, first.ID)
	f.holdAfterWrite.Store(false)
	f.pending.Store(false)
	result := wait(first, "succeeded")
	if result.VolumeResizeIntent == nil || result.VolumeResizeIntent.CurrentGiB != 60 || result.VolumeResizeIntent.TargetGiB != 120 {
		t.Fatalf("wrong persisted intent: %+v", result.VolumeResizeIntent)
	}
	if f.vendor == provider.HuaweiECS && f.jobReads.Load() < 2 {
		t.Fatalf("Huawei job not verified: %d reads", f.jobReads.Load())
	}
	if _, _, err := assets.ScanPage(ctx, scan, staleAssetDiscovery{item: provider.Object{ID: f.id}}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("pre-resize scan not fenced: %v", err)
	}
	assertVolumeResizePublication(t, db, first.ID, f.id, 60, 120)

	f.loseReply.Store(true)
	second := submit("project", "unknown-reply", "1", payload(120, 180), http.StatusAccepted)
	approve(second)
	if op := wait(second, "needs_review"); op.ProviderRequestID != "" || f.sendCount.Load() != 2 {
		t.Fatalf("ambiguous resize: %+v, writes=%d", op, f.sendCount.Load())
	}
	if _, err := store.Resolve(ctx, volumeResizeReviewer, scope, second.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/manual-claim"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success could bypass evidence: %v", err)
	}
	f.wrongRead.Store(false)
	retryNameChangeObservation(t, store, scope, volumeResizeReviewer, second.ID)
	wait(second, "succeeded")
	if got := f.sendCount.Load(); got != 2 {
		t.Fatalf("review replayed remote mutation: %d", got)
	}
	assertVolumeResizePublication(t, db, second.ID, f.id, 120, 180)
}

func startVolumeResizeWorker(t *testing.T, store operations.Store, f *volumeResizeFixture, enabled *atomic.Bool) {
	t.Helper()
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveResize(t, w, r) }))
	t.Cleanup(cloud.Close)
	endpoint, err := url.Parse(cloud.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, cloud.Client().Transport}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, AllowVolumeResize: enabled.Load()}
			if f.vendor == provider.HuaweiECS {
				config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
			} else {
				config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
			}
			client, err := publiccloud.Connect(ctx, binding.Provider, config, transport)
			if err != nil {
				return nil, err
			}
			if client.Identity() != binding.RemoteIdentity {
				return nil, identity.ErrForbidden
			}
			return client.InstanceOperator(), nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("volume resize worker did not stop")
		}
	})
}

func assertVolumeResizePublication(t *testing.T, db *sql.DB, operationID, diskID string, current, target int) {
	t.Helper()
	var capacity, history, outbox, audit int
	var class string
	err := db.QueryRowContext(t.Context(), `SELECT (o.volume_details->>'size_gib')::integer,o.volume_details->>'storage_class',
		(SELECT count(*) FROM cmp_inventory_history WHERE (scope_id,external_id)=(o.scope_id,o.external_id)
		 AND (previous_volume_details->>'size_gib',volume_details->>'size_gib')=($3::integer::text,$4::integer::text)),
		(SELECT count(*) FROM cmp_operation_event_outbox AS box JOIN cmp_instance_operation_events AS ev ON ev.event_id=box.sequence
		 WHERE ev.operation_id=$2 AND box.event_type='volume.operation.succeeded'),
		(SELECT count(*) FROM cmp_project_audit WHERE subject_id=$2 AND category='operation' AND action='resize-volume' AND outcome='succeeded')
		FROM cmp_inventory_objects AS o WHERE o.external_id=$1`, diskID, operationID, current, target).
		Scan(&capacity, &class, &history, &outbox, &audit)
	if err != nil || capacity != target || class != "fixture-class" || history != 1 || outbox != 1 || audit != 1 {
		t.Fatalf("publication capacity=%d class=%q history=%d outbox=%d audit=%d err=%v", capacity, class, history, outbox, audit, err)
	}
}
