package main

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

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudVolumeRelease(t *testing.T) {
	for _, target := range []struct{ vendor, account, region, disk, snapshot, class, state string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "disk-fixture", "snap-fixture", "PRIVATE_SNAPSHOT", "UNATTACHED"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "d-fixture", "s-fixture", "standard", "Available"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "30000000-0000-4000-8000-000000000010", "40000000-0000-4000-8000-000000000010", "standard", "available"},
	} {
		t.Run(target.vendor, func(t *testing.T) {
			t.Parallel()
			f := &volumeReleaseFixture{volumeResizeFixture: volumeResizeFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: target.vendor, account: target.account, region: target.region},
				id:                 target.disk, snapshot: target.snapshot, available: target.state,
			}}
			testPublicCloudVolumeRelease(t, f, target.class)
		})
	}
}

func testPublicCloudVolumeRelease(t *testing.T, f *volumeReleaseFixture, snapshotClass string) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region, volume: f.id, id: f.snapshot, class: snapshotClass},
		f.remoteIdentity(), volumeResizeActor, volumeResizeReviewer)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_inventory_objects SET status=$2,removed_at=NULL,
		volume_details='{"size_gib":60,"storage_class":"fixture-class","usage":"data","bootable":false}'::jsonb WHERE external_id=$1`, f.id, f.available); err != nil {
		t.Fatal(err)
	}
	f.size.Store(60)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	store, assets := operations.NewStore(db), inventory.NewStore(db)
	var enabled atomic.Bool
	startVolumeReleaseWorker(t, store, f, &enabled)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	input := operations.VolumeReleaseInput{Account: f.vendor, Region: f.region, AssetID: f.id, SizeGiB: 60, ConfirmAssetID: f.id}
	payload := func(input operations.VolumeReleaseInput) []byte {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	submit := func(project, key, revision string, body []byte, status int) operations.Operation {
		t.Helper()
		base := "/v1/tenants/tenant/projects/" + project
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, base+"/volumes/release", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+volumeResizeActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", `"`+revision+`"`)
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s status=%d want=%d body=%s", key, w.Code, status, w.Body)
		}
		var op operations.Operation
		if status == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.Action != "release-volume" || op.ResourceKind != provider.Volume || w.Header().Get("Location") != base+"/operations/"+op.ID {
				t.Fatalf("unexpected receipt %+v", op)
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
	body := payload(input)
	wrong := input
	wrong.ConfirmAssetID = "other-disk"
	submit("project", "bad-confirmation", "1", payload(wrong), http.StatusBadRequest)
	submit("other", "wrong-project", "1", body, http.StatusForbidden)
	submit("project", "stale-revision", "2", body, http.StatusPreconditionFailed)
	wrong = input
	wrong.SizeGiB++
	submit("project", "stale-capacity", "1", payload(wrong), http.StatusPreconditionFailed)

	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"preview-volume-release", "tenant", "project", "--account", f.vendor,
		"--region", f.region, "--asset-id", f.id}, local.URL, volumeResizeActor, nil, &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var preview operations.VolumeReleasePreview
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil || !preview.Snapshots || !preview.DataDisk ||
		preview.Attached || preview.SizeGiB != 60 || preview.AssetID != f.id {
		t.Fatalf("unexpected dependency preview %s %v", &output, err)
	}
	submit("project", "local-snapshot", "1", body, http.StatusConflict)
	if _, err := db.ExecContext(ctx, "UPDATE cmp_inventory_objects SET removed_at=now() WHERE external_id=$1", f.snapshot); err != nil {
		t.Fatal(err)
	}

	disabled := submit("project", "disabled", "1", body, http.StatusAccepted)
	approve(disabled)
	if op := wait(disabled, "failed"); op.OutcomeReason != "volume_release_disabled" {
		t.Fatalf("disabled write %+v", op)
	}
	enabled.Store(true)
	f.snapshotPresent.Store(true)
	blocked := submit("project", "cloud-snapshot", "1", body, http.StatusAccepted)
	approve(blocked)
	if op := wait(blocked, "failed"); op.OutcomeReason != "volume_release_preconditions_unconfirmed" || f.sendCount.Load() != 0 {
		t.Fatalf("cloud snapshot did not block release %+v", op)
	}
	f.snapshotPresent.Store(false)

	output.Reset()
	if err := runOperationsClient(ctx, []string{"release-volume", "tenant", "project", "--if-match", "1", "--idempotency-key", "release"},
		local.URL, volumeResizeActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil || op.ID == "" || op.Status != "awaiting_approval" {
		t.Fatalf("unexpected CLI receipt %s %v", &output, err)
	}
	if replay := submit("project", "release", "1", body, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("key replay changed operation %+v", replay)
	}
	if _, err := store.Approve(ctx, volumeResizeActor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) ||
		f.sendCount.Load() != 0 {
		t.Fatalf("write could occur without independent approval %v", err)
	}
	if _, err := store.SubmitVolumeResize(ctx, volumeResizeActor, scope, "resize-conflict", 1,
		operations.VolumeResizeInput{Account: f.vendor, Region: f.region, AssetID: f.id, CurrentGiB: 60, TargetGiB: 120}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("resize bypassed active release %v", err)
	}
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.Volume}
	earlier, err := assets.Claim(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approve(op)
	review := wait(op, "needs_review")
	var absences int
	if err := db.QueryRowContext(ctx, "SELECT absence_observations FROM cmp_network_intents WHERE operation_id=$1", op.ID).Scan(&absences); err != nil ||
		absences != 0 || f.sendCount.Load() != 1 {
		t.Fatalf("incomplete lookup did not reset evidence: absences=%d sends=%d err=%v", absences, f.sendCount.Load(), err)
	}
	receipt := ""
	if f.vendor == provider.HuaweiECS {
		receipt = volumeReleaseReceipt
	}
	if review.ProviderRequestID != receipt {
		t.Fatalf("unexpected receipt %+v", review)
	}
	if _, err := store.Resolve(ctx, volumeResizeReviewer, scope, op.ID, operations.Resolution{Decision: "succeeded",
		EvidenceRef: "fixture/unsafe-manual-success", ProviderRequestID: receipt}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed vendor evidence %v", err)
	}
	f.settled.Store(true)
	retryNameChangeObservation(t, store, scope, volumeResizeReviewer, op.ID)
	finished := wait(op, "succeeded")
	if finished.VolumeReleaseIntent == nil || finished.VolumeReleaseIntent.SizeGiB != 60 || f.sendCount.Load() != 1 ||
		(f.vendor == provider.HuaweiECS && f.jobReads.Load() < 2) {
		t.Fatalf("wrong release completion %+v", finished)
	}
	assertReleaseProjection(t, db, f.id, op.ID)
	restored := provider.Object{ID: f.id, Name: "recovered volume", Status: f.available, Volume: &provider.VolumeDetails{StorageClass: "fixture-class"}}
	if _, _, err := assets.ScanPage(ctx, earlier, staleAssetDiscovery{item: restored}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("old inventory scan could reintroduce released disk %v", err)
	}
	latest, err := assets.Claim(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := assets.ScanPage(ctx, latest, staleAssetDiscovery{item: restored}); err != nil {
		t.Fatal(err)
	}
	var visible bool
	if err := db.QueryRowContext(ctx, "SELECT removed_at IS NULL FROM cmp_inventory_objects WHERE external_id=$1", f.id).Scan(&visible); err != nil || !visible {
		t.Fatalf("fresh restored disk did not reappear: %v %v", visible, err)
	}
}

func startVolumeReleaseWorker(t *testing.T, store operations.Store, f *volumeReleaseFixture, enabled *atomic.Bool) {
	t.Helper()
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveRelease(t, w, r) }))
	t.Cleanup(cloud.Close)
	endpoint, err := url.Parse(cloud.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, cloud.Client().Transport}
	worker, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			profile := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, AllowVolumeRelease: enabled.Load()}
			if f.vendor == provider.HuaweiECS {
				profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
			} else {
				profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
			}
			client, err := publiccloud.Connect(ctx, binding.Provider, profile, transport)
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
			t.Error("volume release worker did not stop")
		}
	})
}

func assertReleaseProjection(t *testing.T, db *sql.DB, diskID, operationID string) {
	t.Helper()
	var removed, permanent bool
	var history, events int
	err := db.QueryRowContext(t.Context(), `SELECT removed_at IS NOT NULL, deletion_confirmed_at IS NOT NULL,
		(SELECT count(*) FROM cmp_inventory_history AS h WHERE (h.scope_id,h.external_id)=(o.scope_id,o.external_id) AND h.action='removed'),
		(SELECT count(*) FROM cmp_operation_event_outbox AS box JOIN cmp_instance_operation_events AS ev ON ev.event_id=box.sequence
		    WHERE ev.operation_id=$2 AND box.event_type='volume.operation.succeeded')
		FROM cmp_inventory_objects AS o WHERE external_id=$1`, diskID, operationID).Scan(&removed, &permanent, &history, &events)
	if err != nil || !removed || permanent || history != 1 || events != 1 {
		t.Fatalf("release projection removed=%v permanent=%v history=%d events=%d err=%v", removed, permanent, history, events, err)
	}
}
