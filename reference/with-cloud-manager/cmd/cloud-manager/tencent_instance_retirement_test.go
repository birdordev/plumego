package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceTencentInstanceRetirement(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	f := newRetirementFixture()
	seedInstanceRetirement(t, db, f)
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(cloud.Close)
	endpoint, err := url.Parse(cloud.URL)
	if err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	store, assets := operations.NewStore(db), inventory.NewStore(db)
	var enabled atomic.Bool
	startRetirementWorker(t, store, f, cloudFixtureTransport{endpoint, cloud.Client().Transport}, &enabled)

	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const projects = "/v1/tenants/tenant/projects/"
	const path = "/instance-operations"
	input := operations.InstanceRetirementInput{Account: f.vendor, Region: f.region, AssetID: retirementInstance,
		SystemDiskID: retirementDisk, ConfirmID: retirementInstance}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, project, suffix, token, key, revision string, body []byte, code int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, method, projects+project+path+suffix, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != code {
			t.Fatalf("%s %s %s got %d, want %d: %s", method, suffix, key, w.Code, code, w.Body)
		}
		return w
	}
	submit := func(key string) operations.Operation {
		t.Helper()
		resp := request(http.MethodPost, "project", "/retire", retirementRequester, key, `"1"`, payload, http.StatusAccepted)
		var op operations.Operation
		decodeProjectData(t, resp, &op)
		if op.ID == "" || op.Action != "retire-instance" || op.Status != "awaiting_approval" ||
			resp.Header().Get("Location") != projects+"project"+path+"/"+op.ID {
			t.Fatalf("invalid retirement receipt: %+v", op)
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, retirementReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, retirementRequester, op.ID, state, true)
	}

	var previewOutput bytes.Buffer
	if err := runOperationsClient(ctx, []string{"preview-retire", "tenant", "project", "--account", f.vendor, "--region", f.region,
		"--asset-id", retirementInstance}, server.URL, retirementRequester, nil, &previewOutput, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var preview operations.InstanceRetirementPreview
	if err := json.Unmarshal(previewOutput.Bytes(), &preview); err != nil || preview.SystemDiskID != retirementDisk || preview.OwnershipRevision != 1 {
		t.Fatalf("invalid preview: %s %v", &previewOutput, err)
	}
	staleDisk := bytes.Replace(payload, []byte(retirementDisk), []byte("disk-wrong"), 1)
	wrongConfirm := bytes.Replace(payload, []byte(`"confirm_asset_id":"ins-retire"`), []byte(`"confirm_asset_id":"ins-wrong"`), 1)
	request(http.MethodPost, "other", "/retire", retirementRequester, "other-project", `"1"`, payload, http.StatusForbidden)
	request(http.MethodPost, "project", "/retire", retirementRequester, "stale-revision", `"2"`, payload, http.StatusPreconditionFailed)
	request(http.MethodPost, "project", "/retire", retirementRequester, "stale-disk", `"1"`, staleDisk, http.StatusPreconditionFailed)
	request(http.MethodPost, "project", "/retire", retirementRequester, "wrong-confirmation", `"1"`, wrongConfirm, http.StatusBadRequest)
	if _, err := store.Submit(ctx, retirementRequester, scope, "old-delete", 1, operations.Input{
		Account: f.vendor, Region: f.region, AssetID: retirementInstance, Action: "delete",
	}); !errors.Is(err, operations.ErrInvalid) {
		t.Fatalf("disk-preserving delete accepted Tencent operation: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_protected_instances(scope_id,external_id)
		SELECT scope_id,external_id FROM cmp_inventory_objects WHERE external_id=$1`, retirementInstance); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "project", "/retire", retirementRequester, "protected", `"1"`, payload, http.StatusConflict)
	if _, err := db.ExecContext(ctx, `DELETE FROM cmp_protected_instances WHERE external_id=$1`, retirementInstance); err != nil {
		t.Fatal(err)
	}

	disabled := submit("disabled")
	if _, err := store.Approve(ctx, retirementRequester, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester approved own retirement: %v", err)
	}
	approve(disabled)
	if op := wait(disabled, "failed"); op.OutcomeReason != "instance_retirement_disabled" {
		t.Fatalf("unexpected disabled result: %+v", op)
	}
	enabled.Store(true)
	for _, scenario := range []struct {
		key       string
		configure func(bool)
	}{
		{"cloud-protected", f.protected.Store},
		{"unknown-disk", f.extraDisk.Store},
		{"active-instance", func(enabled bool) {
			state := "STOPPED"
			if enabled {
				state = "RUNNING"
			}
			f.state.Store(state)
		}},
		{"prepaid-instance", func(enabled bool) {
			charge := "POSTPAID_BY_HOUR"
			if enabled {
				charge = "PREPAID"
			}
			f.charge.Store(charge)
		}},
	} {
		scenario.configure(true)
		op := submit(scenario.key)
		approve(op)
		if result := wait(op, "failed"); result.OutcomeReason != "instance_retirement_preconditions_unconfirmed" || f.writes.Load() != 0 {
			t.Fatalf("unsafe instance reached dispatch: %+v", result)
		}
		scenario.configure(false)
	}

	f.pauseDisk.Store(true)
	revoked := submit("approval-revoked")
	approve(revoked)
	select {
	case <-f.diskRead:
	case <-time.After(7 * time.Second):
		t.Fatal("retirement preflight did not start")
	}
	if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET active=false,revision=revision+1
		WHERE tenant_id='tenant' AND project_id='project' AND principal_id='reviewer'`); err != nil {
		t.Fatal(err)
	}
	close(f.resumeDisk)
	if result := wait(revoked, "failed"); result.OutcomeReason != "dispatch_unavailable" || f.writes.Load() != 0 {
		t.Fatalf("revoked approval reached dispatch: %+v", result)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET active=true,revision=revision+1
		WHERE tenant_id='tenant' AND project_id='project' AND principal_id='reviewer'`); err != nil {
		t.Fatal(err)
	}

	f.dropReply.Store(true)
	unknown := submit("lost-reply")
	approve(unknown)
	if result := wait(unknown, "needs_review"); result.OutcomeReason != "instance_retirement_receipt_required" ||
		result.ProviderRequestID != "" || f.writes.Load() != 1 {
		t.Fatalf("lost reply was incorrectly retried or confirmed: %+v", result)
	}
	evidence := operations.ObservationRetry{EvidenceRef: "fixture/cam-audit"}
	if _, err := store.RetryCreationObservation(ctx, retirementReviewer, scope, unknown.ID, evidence); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("observation restarted without a receipt: %v", err)
	}
	if _, err := store.Resolve(ctx, retirementReviewer, scope, unknown.ID, operations.Resolution{
		Decision: "failed", EvidenceRef: "fixture/no-effect-verified",
	}); err != nil {
		t.Fatal(err)
	}
	f.dropReply.Store(false)

	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"retire-instance", "tenant", "project", "--if-match", "1", "--idempotency-key", "retire"},
		server.URL, retirementRequester, bytes.NewReader(payload), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil || op.Status != "awaiting_approval" {
		t.Fatalf("invalid CLI receipt: %s %v", &output, err)
	}
	if replay := submit("retire"); replay.ID != op.ID {
		t.Fatal("idempotent retry produced another operation")
	}
	request(http.MethodPost, "project", "/retire", retirementRequester, "busy", `"1"`, payload, http.StatusConflict)
	if _, err := store.SubmitSnapshotCreation(ctx, retirementRequester, scope, "system-disk-snapshot", 1,
		operations.SnapshotInput{Account: f.vendor, Region: f.region, SnapshotSpec: provider.SnapshotSpec{
			VolumeID: retirementDisk, Name: "system_disk_snapshot", AllowAttached: true,
		}}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("retirement did not block conflicting snapshot: %v", err)
	}
	for _, item := range []struct {
		kind provider.Kind
		id   string
	}{{provider.Instance, retirementInstance}, {provider.Volume, retirementDisk}} {
		if _, err := assets.TransferOwnership(ctx, retirementRequester, scope, 1, inventory.OwnershipTransfer{
			Account: f.vendor, Region: f.region, Kind: item.kind, AssetID: item.id, TargetProject: "other",
		}); !errors.Is(err, identity.ErrAccountConflict) {
			t.Fatalf("%s ownership was not reserved: %v", item.kind, err)
		}
	}
	claim, err := assets.Claim(ctx, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.Instance}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	approve(op)
	waitRetirementRound(t, db, op.ID, 1, 0)
	waitRetirementRound(t, db, op.ID, 2, 0)
	f.state.Store("absent")
	f.statusMode.Store(1)
	waitRetirementRound(t, db, op.ID, 3, 0)
	f.statusMode.Store(0)
	waitRetirementRound(t, db, op.ID, 4, 1)
	f.statusMode.Store(2)
	waitRetirementRound(t, db, op.ID, 5, 0)
	f.statusMode.Store(3)
	op = wait(op, "needs_review")
	if op.ProviderRequestID != retirementReceipt || op.InstanceRetirementIntent == nil || op.InstanceRetirementIntent.SystemDiskID != retirementDisk ||
		op.InstanceRetirementIntent.DiskOwnershipRevision != 1 || f.writes.Load() != 2 {
		t.Fatalf("lost retirement evidence or repeated dispatch: %+v", op)
	}
	f.statusMode.Store(0)
	enabled.Store(false)
	retryRetirementObservation(t, store, scope, op.ID, evidence)
	waitRetirementRound(t, db, op.ID, 1, 1)
	wait(op, "succeeded")
	if _, _, err := assets.ScanPage(ctx, claim, staleAssetDiscovery{item: provider.Object{ID: retirementInstance,
		Name: "stale fixture", Status: "STOPPED"}}); err != nil && !errors.Is(err, inventory.ErrStale) {
		t.Fatal(err)
	}
	assertRetirementPublished(t, db, op.ID)
	if f.writes.Load() != 2 {
		t.Fatal("read-only recovery dispatched another mutation")
	}
	if _, err := store.PreviewInstanceRetirement(ctx, retirementRequester, scope, f.vendor, f.region, retirementInstance); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("finished instance remained eligible: %v", err)
	}
	spec := httptest.NewRecorder()
	app.ServeHTTP(spec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if spec.Code != http.StatusOK || !strings.Contains(spec.Body.String(), "retireTencentInstance") ||
		!strings.Contains(spec.Body.String(), "previewTencentInstanceRetirement") {
		t.Fatalf("retirement operations missing in OpenAPI: HTTP %d", spec.Code)
	}
}
