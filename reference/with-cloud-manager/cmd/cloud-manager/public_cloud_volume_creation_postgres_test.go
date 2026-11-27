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

const volumeCreateActor = "fixture-volume-create-executor-token"
const volumeCreateReviewer = "fixture-volume-create-reviewer-token"

func TestAcceptancePublicCloudVolumeCreation(t *testing.T) {
	for _, target := range []struct{ vendor, account, region, zone, class, first, second string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "ap-guangzhou-2", "CLOUD_SSD", "disk-first", "disk-second"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "cn-hangzhou-g", "cloud_essd", "d-first", "d-second"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "cn-north-4a", "GPSSD", "50000000-0000-4000-8000-000000000031", "50000000-0000-4000-8000-000000000032"},
	} {
		t.Run(target.vendor, func(t *testing.T) {
			t.Parallel()
			f := &volumeCreationFixture{vpcCreationFixture: vpcCreationFixture{vendor: target.vendor, account: target.account, region: target.region},
				zone: target.zone, class: target.class, firstID: target.first, secondID: target.second}
			testPublicCloudVolumeCreation(t, f)
		})
	}
}

func testPublicCloudVolumeCreation(t *testing.T, f *volumeCreationFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region, volume: "fixture-source", id: "fixture-snapshot", class: "standard"},
		f.remoteIdentity(), volumeCreateActor, volumeCreateReviewer)
	_, err := db.ExecContext(ctx, `WITH placement AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'zone',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$3,$3,'available',1 FROM placement`, f.vendor, f.region, f.zone)
	if err != nil {
		t.Fatal(err)
	}

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	store, assets := operations.NewStore(db), inventory.NewStore(db)
	var capGiB atomic.Int32
	startVolumeCreationWorker(t, store, f, &capGiB)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	input := operations.VolumeCreationInput{Account: f.vendor, Region: f.region,
		VolumeSpec: provider.VolumeSpec{Name: volumeCreationName, Zone: f.zone, DiskType: f.class, SizeGiB: 40, Encrypted: true}}
	body := func(in operations.VolumeCreationInput) []byte {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	submit := func(project, key string, payload []byte, code int) operations.Operation {
		t.Helper()
		base := "/v1/tenants/tenant/projects/" + project
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, base+"/volumes", bytes.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+volumeCreateActor)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("create disk %s: %d want %d %s", key, w.Code, code, w.Body.String())
		}
		var op operations.Operation
		if code == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.Action != "create-volume" || op.ResourceKind != provider.Volume || op.AssetID != "" ||
				w.Header().Get("Location") != base+"/operations/"+op.ID {
				t.Fatalf("wrong creation receipt %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, volumeCreateReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, volumeCreateActor, op.ID, state, true)
	}

	submit("other", "wrong-project", body(input), http.StatusForbidden)
	wrong := input
	wrong.Zone += "-unknown"
	submit("project", "unknown-placement", body(wrong), http.StatusPreconditionFailed)
	wrong = input
	wrong.DiskType = "network-disk-unknown"
	submit("project", "bad-class", body(wrong), http.StatusBadRequest)
	blocked := submit("project", "disabled", body(input), http.StatusAccepted)
	approve(blocked)
	if result := wait(blocked, "failed"); result.OutcomeReason != "provider_creation_blocked" || f.sends.Load() != 0 {
		t.Fatalf("disabled account dispatched: %+v", result)
	}
	capGiB.Store(100)
	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"create-volume", "tenant", "project", "--idempotency-key", "first"},
		local.URL, volumeCreateActor, bytes.NewReader(body(input)), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var first operations.Operation
	if err := json.Unmarshal(output.Bytes(), &first); err != nil || first.ID == "" || first.Status != "awaiting_approval" {
		t.Fatalf("CLI receipt %s %v", output.String(), err)
	}
	if _, err := store.Approve(ctx, volumeCreateActor, scope, first.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester can self approve: %v", err)
	}
	if submit("project", "first", body(input), http.StatusAccepted).ID != first.ID || f.sends.Load() != 0 {
		t.Fatal("duplicate or premature dispatch")
	}
	wrong = input
	wrong.SizeGiB = 80
	submit("project", "first", body(wrong), http.StatusConflict)
	early, err := assets.Claim(ctx, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.Volume}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approve(first)
	op := wait(first, "succeeded")
	if op.ProviderRequestID != "fixture-create" || op.AssetID != f.firstID || op.VolumeCreationIntent == nil || *op.VolumeCreationIntent != input.VolumeSpec {
		t.Fatalf("incorrect observed creation %+v", op)
	}
	if _, _, err := assets.ScanPage(ctx, early, staleAssetDiscovery{item: provider.Object{ID: f.firstID}}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("older scan could change new volume: %v", err)
	}
	assertCreatedVolume(t, db, op, f)

	f.loseReply.Store(true)
	f.wrongCapacity.Store(true)
	second := submit("project", "lost-reply", body(input), http.StatusAccepted)
	approve(second)
	if op := wait(second, "needs_review"); op.ProviderRequestID != "" || op.AssetID != "" || f.sends.Load() != 2 {
		t.Fatalf("ambiguous creation did not require evidence: %+v sends=%d", op, f.sends.Load())
	}
	f.wrongCapacity.Store(false)
	retryNameChangeObservation(t, store, scope, volumeCreateReviewer, second.ID)
	op = wait(second, "succeeded")
	if f.sends.Load() != 2 || op.AssetID != f.secondID {
		t.Fatalf("recovery repeated allocation: %+v sends=%d", op, f.sends.Load())
	}
	assertCreatedVolume(t, db, op, f)
}

func startVolumeCreationWorker(t *testing.T, store operations.Store, f *volumeCreationFixture, maxGiB *atomic.Int32) {
	t.Helper()
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveVolume(t, w, r) }))
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
			profile := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, VolumeCreateMaxGiB: int(maxGiB.Load())}
			if f.vendor == provider.HuaweiECS {
				profile.ProjectID, profile.IAMToken, profile.HuaweiVolumeKMSKeyID = fixtureHuaweiProject, fixtureHuaweiIAM, volumeCreationKMS
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
			t.Error("volume creation worker did not stop")
		}
	})
}

func assertCreatedVolume(t *testing.T, db *sql.DB, op operations.Operation, f *volumeCreationFixture) {
	t.Helper()
	var project, status, class, zone string
	var size, events, links int
	err := db.QueryRowContext(t.Context(), `SELECT own.project_id,o.status,o.volume_details->>'storage_class',
		o.links->0->>'id', (o.volume_details->>'size_gib')::integer,jsonb_array_length(o.links),
		(SELECT count(*) FROM cmp_operation_event_outbox AS box JOIN cmp_instance_operation_events AS ev ON ev.event_id=box.sequence
		WHERE ev.operation_id=$1 AND box.event_type='volume.operation.succeeded')
		FROM cmp_inventory_objects AS o JOIN cmp_asset_ownership AS own USING(scope_id,external_id)
		WHERE o.external_id=$2`, op.ID, op.AssetID).Scan(&project, &status, &class, &zone, &size, &links, &events)
	if err != nil || project != "project" || status == "" || class != f.class || zone != f.zone || size != 40 || links != 1 || events != 1 {
		t.Fatalf("created volume projection: project=%q status=%q class=%q zone=%q size=%d links=%d events=%d err=%v",
			project, status, class, zone, size, links, events, err)
	}
}
