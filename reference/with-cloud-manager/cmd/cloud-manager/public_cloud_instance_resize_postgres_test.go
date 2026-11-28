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
	"slices"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const instanceResizeActor = "fixture-instance-resize-operator-token"
const instanceResizeReviewer = "fixture-instance-resize-reviewer-token"

func TestAcceptancePublicCloudInstanceResize(t *testing.T) {
	for _, target := range []struct{ vendor, account, region, id, original, target string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "ins-fixture", "S5.MEDIUM4", "S5.LARGE8"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "i-fixture", "ecs.g6.large", "ecs.g6.xlarge"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "30000000-0000-4000-8000-000000000010", "c6.large.2", "c6.xlarge.2"},
	} {
		t.Run(target.vendor, func(t *testing.T) {
			t.Parallel()
			f := &instanceResizeFixture{vpcCreationFixture: vpcCreationFixture{vendor: target.vendor, account: target.account, region: target.region},
				id: target.id, original: target.original, target: target.target}
			testPublicCloudInstanceResize(t, f)
		})
	}
}

func testPublicCloudInstanceResize(t *testing.T, f *instanceResizeFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region, volume: "seed-disk", id: "seed-snapshot", class: "standard"},
		f.remoteIdentity(), instanceResizeActor, instanceResizeReviewer)
	ops, assets := operations.NewStore(db), inventory.NewStore(db)
	connect := startInstanceResizeWorker(t, ops, f)
	client, err := connect(ctx, identity.CloudAccountBinding{Provider: f.vendor, RemoteIdentity: f.remoteIdentity()})
	if err != nil {
		t.Fatal(err)
	}
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.Instance}
	scan, err := assets.ClaimWithCredentialRevision(ctx, key, time.Minute, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, done, err := assets.ScanPage(ctx, scan, client); err != nil || !done {
		t.Fatalf("discovery did not finish: done=%v err=%v", done, err)
	}

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	project := identity.Scope{Tenant: "tenant", Project: "project"}
	list := inventory.Query{Tenant: project.Tenant, Project: project.Project, Kind: provider.Instance, Search: f.original, Limit: 10}
	asset, err := assets.List(ctx, instanceResizeActor, list)
	if err != nil || len(asset.Items) != 1 || asset.Items[0].InstanceType != f.original {
		t.Fatalf("instance type was not discovered and searchable: %+v %v", asset, err)
	}
	input := operations.InstanceResizeInput{Account: f.vendor, Region: f.region, AssetID: f.id, CurrentType: f.original,
		TargetType: f.target, AcceptAutoStart: f.vendor == provider.TencentCVM}
	submit := func(project, idempotency, revision string, payload operations.InstanceResizeInput, expected int) operations.Operation {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/tenants/tenant/projects/"+project+"/instances/resize", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+instanceResizeActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotency)
		req.Header.Set("If-Match", `"`+revision+`"`)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != expected {
			t.Fatalf("%s: HTTP %d, expected %d: %s", idempotency, w.Code, expected, w.Body)
		}
		var op operations.Operation
		if expected == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.Action != "resize-instance" || op.ResourceKind != provider.Instance || op.Status != "awaiting_approval" {
				t.Fatalf("unexpected receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := ops.Approve(ctx, instanceResizeReviewer, project, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, ops, project, instanceResizeActor, op.ID, status, true)
	}

	submit("other", "wrong-project", "1", input, http.StatusForbidden)
	submit("project", "stale-ownership", "2", input, http.StatusPreconditionFailed)
	mismatch := input
	mismatch.CurrentType = "stale.fixture.type"
	submit("project", "stale-type", "1", mismatch, http.StatusPreconditionFailed)
	mismatch = input
	mismatch.AcceptAutoStart = !mismatch.AcceptAutoStart
	submit("project", "incorrect-auto-start-consent", "1", mismatch, http.StatusBadRequest)

	disabled := submit("project", "disabled", "1", input, http.StatusAccepted)
	approve(disabled)
	if op := wait(disabled, "failed"); op.OutcomeReason != "instance_resize_disabled" || f.sends.Load() != 0 {
		t.Fatalf("disabled operation dispatched: %+v", op)
	}
	f.enabled.Store(true)
	f.prepaid.Store(true)
	prepaid := submit("project", "prepaid", "1", input, http.StatusAccepted)
	approve(prepaid)
	if op := wait(prepaid, "failed"); op.OutcomeReason != "instance_resize_preconditions_unconfirmed" || f.sends.Load() != 0 {
		t.Fatalf("prepaid operation dispatched: %+v", op)
	}
	f.prepaid.Store(false)

	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	testInstanceResizeOptions(t, db, local, f, connect)
	f.optionsUnavailable.Store(true)
	unavailable := submit("project", "remote-option-no-longer-available", "2", input, http.StatusAccepted)
	approve(unavailable)
	if op := wait(unavailable, "failed"); op.OutcomeReason != "instance_resize_preconditions_unconfirmed" || f.sends.Load() != 0 {
		t.Fatalf("unavailable option dispatched: %+v", op)
	}
	f.optionsUnavailable.Store(false)
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"resize-instance", "tenant", "project", "--idempotency-key", "resize", "--if-match", "2"},
		local.URL, instanceResizeActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil || op.Status != "awaiting_approval" || op.ID == "" {
		t.Fatalf("unexpected CLI receipt: %s %v", output.String(), err)
	}
	if _, err := ops.Approve(ctx, instanceResizeActor, project, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester approved own resize: %v", err)
	}
	if replay := submit("project", "resize", "2", input, http.StatusAccepted); replay.ID != op.ID || f.sends.Load() != 0 {
		t.Fatalf("wrong replay or write before independent approval: %+v", replay)
	}
	mismatch = input
	mismatch.TargetType = "other.fixture.type"
	submit("project", "resize", "2", mismatch, http.StatusConflict)
	power := operations.Input{Account: f.vendor, Region: f.region, AssetID: f.id, Action: "start"}
	if _, err := ops.Submit(ctx, instanceResizeActor, project, "blocked-power", 2, power); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("power change could cross resize reservation: %v", err)
	}
	staleScan, err := assets.ClaimWithCredentialRevision(ctx, key, time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.pending.Store(true)
	f.loseReply.Store(f.vendor != provider.HuaweiECS)
	approve(op)
	observing := wait(op, "observing")
	wantReceipt := ""
	if f.vendor == provider.HuaweiECS {
		wantReceipt = instanceResizeReceipt
	}
	if observing.ProviderRequestID != wantReceipt || f.sends.Load() != 1 {
		t.Fatalf("unexpected observation receipt: %+v, sends=%d", observing, f.sends.Load())
	}
	if f.loseReply.Load() {
		waitPendingCloudObservation(t, db, op.ID)
		if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET max_attempts=attempts+1 WHERE id=$1 AND status='queued'`, op.ID); err != nil {
			t.Fatal(err)
		}
		wait(op, "needs_review")
		if _, err := ops.Resolve(ctx, instanceResizeReviewer, project, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/manual"}); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("manual success bypassed evidence: %v", err)
		}
		f.wrongRead.Store(false)
		f.pending.Store(false)
		retryNameChangeObservation(t, ops, project, instanceResizeReviewer, op.ID)
	} else {
		waitPendingCloudObservation(t, db, op.ID)
		if f.jobReads.Load() == 0 {
			t.Fatal("resize polled without reading the Huawei task")
		}
		if _, err := db.ExecContext(ctx, `UPDATE cmp_inventory_objects SET status='RESIZE' WHERE external_id=$1`, f.id); err != nil {
			t.Fatal(err)
		}
		f.pending.Store(false)
	}

	done := wait(op, "succeeded")
	intent := done.InstanceResizeIntent
	if intent == nil || intent.CurrentType != f.original || intent.TargetType != f.target ||
		intent.AcceptAutoStart != input.AcceptAutoStart || intent.OwnershipRevision != 2 || f.sends.Load() != 1 {
		t.Fatalf("wrong persisted intent or repeated write: %+v, sends=%d", intent, f.sends.Load())
	}
	if _, _, err := assets.ScanPage(ctx, staleScan, staleAssetDiscovery{item: provider.Object{ID: f.id, InstanceType: f.original}}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("pre-resize scan was not fenced: %v", err)
	}
	list.Search = f.target
	current, err := assets.List(ctx, instanceResizeActor, list)
	if err != nil || len(current.Items) != 1 || current.Items[0].InstanceType != f.target || current.Items[0].Status != f.observedState() {
		t.Fatalf("resize inventory not reconciled: %+v %v", current, err)
	}
	history, err := assets.History(ctx, instanceResizeActor, list)
	if err != nil || !slices.ContainsFunc(history.Items, func(item inventory.Change) bool {
		return item.PreviousInstanceType == f.original && item.InstanceType == f.target
	}) {
		t.Fatalf("resize inventory history missing: %+v %v", history, err)
	}
	assertInstanceResizePublication(t, db, op.ID)
}

func startInstanceResizeWorker(t *testing.T, store operations.Store, f *instanceResizeFixture) func(context.Context, identity.CloudAccountBinding) (*publiccloud.Client, error) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.handle(t, w, r) }))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, server.Client().Transport}
	connect := func(ctx context.Context, binding identity.CloudAccountBinding) (*publiccloud.Client, error) {
		config := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}
		if f.enabled.Load() {
			config.InstanceResizeSizes = []string{f.target}
		}
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
		return client, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			client, err := connect(ctx, binding)
			if err != nil {
				return nil, err
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
			t.Error("instance resize worker did not stop")
		}
	})
	return connect
}

func assertInstanceResizePublication(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	var eventCount, auditCount int
	err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_operation_event_outbox AS box JOIN cmp_instance_operation_events AS ev ON ev.event_id=box.sequence
		 WHERE ev.operation_id=$1 AND box.event_type='instance.operation.succeeded'),
		(SELECT count(*) FROM cmp_project_audit WHERE subject_id=$1 AND category='operation' AND action='resize-instance' AND outcome='succeeded')`,
		id).Scan(&eventCount, &auditCount)
	if err != nil || eventCount != 1 || auditCount != 1 {
		t.Fatalf("instance resize outbox=%d audit=%d err=%v", eventCount, auditCount, err)
	}
}
