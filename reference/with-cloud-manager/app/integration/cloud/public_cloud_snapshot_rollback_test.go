package cloudtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const fixtureRollbackReceipt = "fixture-rollback-request"

func TestAcceptancePublicCloudSnapshotRollback(t *testing.T) {
	fixtures := []snapshotDeletionFixture{
		{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou", volume: "disk-fixture", id: "snap-fixture", class: "PRIVATE_SNAPSHOT"},
		{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou", volume: "d-fixture", id: "s-fixture", class: "standard"},
		{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010", class: "flash"},
		{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010", class: "standard"},
	}
	for i := range fixtures {
		source := &fixtures[i]
		t.Run(source.vendor+"/"+source.class, func(t *testing.T) { testPublicCloudSnapshotRollback(t, source) })
	}
}

func testPublicCloudSnapshotRollback(t *testing.T, source *snapshotDeletionFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-snapshot-rollback-operator-token", "fixture-snapshot-rollback-reviewer-token"
	identityFixture := vpcCreationFixture{vendor: source.vendor, account: source.account, region: source.region, parent: source.volume}
	seedSnapshotDeletion(t, db, source, identityFixture.remoteIdentity(), actor, reviewer)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'instance',1,now())`, source.vendor, source.region); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'instance-fixture','instance','stopped',1 FROM cmp_inventory_scopes WHERE kind='instance'`); err != nil {
		t.Fatal(err)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &snapshotRollbackFixture{source: source}
	f.snapshots.source.vendor, f.snapshots.source.region = source.vendor, source.region
	f.snapshots.source.volume, f.snapshots.source.id, f.snapshots.source.class = source.volume, source.id, source.class
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
			identityFixture.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected rollback fixture request: %s %s %s", r.Method, r.Host, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer cloud.Close()
	endpoint, err := url.Parse(cloud.URL)
	if err != nil {
		t.Fatal(err)
	}
	config := publiccloud.Config{AccountID: source.account, Regions: []string{source.region}}
	if source.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	var enabled atomic.Bool
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if binding.Provider != source.vendor || binding.RemoteIdentity != identityFixture.remoteIdentity() {
				return nil, identity.ErrForbidden
			}
			profile := config
			profile.AllowSnapshotRollback = enabled.Load()
			return publiccloud.Connect(ctx, binding.Provider, profile, cloudFixtureTransport{endpoint, cloud.Client().Transport})
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("snapshot rollback worker did not stop")
		}
	}()
	input := operations.SnapshotRollback{SnapshotDeletion: operations.SnapshotDeletion{Account: source.vendor,
		Region: source.region, AssetID: source.id, VolumeID: source.volume}, ConfirmOverwrite: true}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const base = "/v1/tenants/tenant/projects/"
	body := func(input operations.SnapshotRollback) []byte {
		t.Helper()
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	submit := func(project, key, revision string, request operations.SnapshotRollback, want int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, base+project+"/snapshots/rollback", bytes.NewReader(body(request)))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("rollback %s: status %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.ResourceKind != provider.Snapshot || op.AssetID != source.id || op.Action != "rollback-snapshot" ||
				w.Header().Get("Location") != base+project+"/operations/"+op.ID {
				t.Fatalf("wrong rollback receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, status, true)
	}
	withoutConsent := input
	withoutConsent.ConfirmOverwrite = false
	submit("project", "no-consent", `"1"`, withoutConsent, http.StatusBadRequest)
	submit("other", "wrong-project", `"1"`, input, http.StatusForbidden)
	if source.vendor == provider.AlibabaECS {
		submit("project", "removed-source", `"1"`, input, http.StatusForbidden)
		if _, err := db.ExecContext(ctx, `UPDATE cmp_inventory_objects SET removed_at=NULL WHERE external_id=$1`, source.volume); err != nil {
			t.Fatal(err)
		}
	}
	submit("project", "stale-owner", `"2"`, input, http.StatusPreconditionFailed)

	disabled := submit("project", "disabled", `"1"`, input, http.StatusAccepted)
	approve(disabled)
	if got := wait(disabled, "failed"); got.OutcomeReason != "snapshot_rollback_disabled" || f.snapshots.reads.Load() != 0 {
		t.Fatalf("disabled operation reached the snapshot API: %+v", got)
	}
	enabled.Store(true)
	f.attached.Store(true)
	attached := submit("project", "attached", `"1"`, input, http.StatusAccepted)
	approve(attached)
	if got := wait(attached, "failed"); got.OutcomeReason != "snapshot_rollback_preconditions_unconfirmed" || f.writes.Load() != 0 {
		t.Fatalf("rollback reached an attached destination: %+v writes=%d", got, f.writes.Load())
	}
	f.attached.Store(false)
	volumeChange := operations.Input{Account: source.vendor, Region: source.region, AssetID: "instance-fixture", Action: "detach-volume",
		VolumeID: source.volume, VolumeRevision: 1, GuestUnmounted: true}
	detach, err := store.Submit(ctx, actor, scope, "detach-first", 1, volumeChange)
	if err != nil || detach.Status != "awaiting_approval" {
		t.Fatalf("detach setup: %+v err=%v", detach, err)
	}
	submit("project", "detach-active", `"1"`, input, http.StatusConflict)
	if _, err := store.Approve(ctx, reviewer, scope, detach.ID, operations.Approval{Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	op := submit("project", "rollback", `"1"`, input, http.StatusAccepted)
	if op.Status != "awaiting_approval" || f.writes.Load() != 0 {
		t.Fatalf("rollback skipped approval: %+v", op)
	}
	if replay := submit("project", "rollback", `"1"`, input, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("rollback replay returned a different operation: %+v", replay)
	}
	if _, err := store.SubmitSnapshotDeletion(ctx, actor, scope, "delete-during-rollback", 1, input.SnapshotDeletion); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("rollback permits concurrent snapshot deletion: %v", err)
	}
	if _, err := store.Submit(ctx, actor, scope, "detach-during-rollback", 1, volumeChange); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("rollback permits concurrent detach: %v", err)
	}
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester approved rollback: %v", err)
	}
	if source.vendor == provider.TencentCVM {
		server := httptest.NewTLSServer(app)
		defer server.Close()
		var reply bytes.Buffer
		args := []string{"rollback-snapshot", "tenant", "project", "--idempotency-key", "rollback", "--if-match", "1"}
		if err := commands.RunOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body(input)), &reply, server.Client().Transport); err != nil || !strings.Contains(reply.String(), op.ID) {
			t.Fatalf("rollback CLI replay: %v %s", err, &reply)
		}
	}
	approve(op)
	standardHuawei := source.vendor == provider.HuaweiECS && source.class == "standard"
	if standardHuawei {
		wait(op, "observing")
		waitPendingCloudObservation(t, db, op.ID)
		f.jobState.Store(1)
	}
	pending := wait(op, "needs_review")
	if f.writes.Load() != 1 || pending.SnapshotIntent == nil || pending.SnapshotIntent.VolumeID != source.volume ||
		pending.SnapshotIntent.Class != source.class || pending.SnapshotIntent.CanRetryObservation != standardHuawei {
		t.Fatalf("rollback evidence or write count: %+v writes=%d", pending, f.writes.Load())
	}
	if (source.vendor == provider.TencentCVM || standardHuawei) != (pending.ProviderRequestID == fixtureRollbackReceipt) {
		t.Fatalf("unexpected provider receipt: %q", pending.ProviderRequestID)
	}
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	retry := operations.ObservationRetry{EvidenceRef: "incident/rollback"}
	if standardHuawei {
		f.jobState.Store(3)
		missingTarget := operations.ObservationRetry{EvidenceRef: "incident/rollback-missing-target"}
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, missingTarget); err != nil {
			t.Fatal(err)
		}
		if pending := wait(op, "needs_review"); !pending.SnapshotIntent.CanRetryObservation {
			t.Fatalf("missing Huawei job target prevented further observation: %+v", pending)
		}
		waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
		f.jobState.Store(2)
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, retry); err != nil {
			t.Fatal(err)
		}
		if completed := wait(op, "succeeded"); completed.OutcomeReason != "snapshot_rollback_job_succeeded" {
			t.Fatalf("valid Huawei job did not confirm completion: %+v", completed)
		}
	} else {
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, retry); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("non-job receipt allowed a task retry: %v", err)
		}
		resolution := operations.Resolution{Decision: "succeeded", EvidenceRef: "incident/rollback-confirmed", ProviderRequestID: pending.ProviderRequestID}
		if _, err := store.Resolve(ctx, actor, scope, op.ID, resolution); !errors.Is(err, identity.ErrForbidden) {
			t.Fatalf("operator resolved own rollback: %v", err)
		}
		result, err := store.Resolve(ctx, reviewer, scope, op.ID, resolution)
		if err != nil || result.Status != "succeeded" || result.OutcomeReason != "manual_review" {
			t.Fatalf("failed to record reviewed rollback: %+v %v", result, err)
		}
	}
	if f.writes.Load() != 1 {
		t.Fatalf("review resent rollback: writes=%d", f.writes.Load())
	}
	assertSnapshotTombstone(t, db, source.id, false)
	assertSnapshotTombstone(t, db, source.volume, false)
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='snapshot.operation.succeeded'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("rollback terminal event: count=%d err=%v", events, err)
	}
}

type snapshotRollbackFixture struct {
	source    *snapshotDeletionFixture
	snapshots snapshotRestoreFixture
	attached  atomic.Bool
	writes    atomic.Int32
	jobState  atomic.Int32
}

func (f *snapshotRollbackFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	source := f.source
	tencentAction, alibabaAction := r.Header.Get("X-TC-Action"), r.Header.Get("X-ACS-Action")
	if tencentAction == "DescribeSnapshots" || alibabaAction == "DescribeSnapshots" || strings.HasSuffix(r.URL.Path, "/snapshots/detail") ||
		strings.HasSuffix(r.URL.Path, "/cloudsnapshots/detail") {
		return f.snapshots.serve(t, w, r)
	}
	send := func(value any) { testsupport.WriteJSON(t, w, value) }
	switch source.vendor {
	case provider.TencentCVM:
		raw, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPost || r.Host != "cbs.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != source.region ||
			r.Header.Get("X-TC-Version") != "2017-03-12" || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
			!validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		switch tencentAction {
		case "DescribeDisks":
			if string(raw) != fmt.Sprintf(`{"DiskIds":[%q]}`, source.volume) {
				return false
			}
			state := "UNATTACHED"
			if f.attached.Load() {
				state = "ATTACHED"
			}
			send(map[string]any{"Response": map[string]any{"TotalCount": 1, "DiskSet": []any{
				map[string]any{"DiskId": source.volume, "DiskState": state, "DiskUsage": "DATA_DISK", "DiskSize": 40}}}})
		case "ApplySnapshot":
			if string(raw) != fmt.Sprintf(`{"DiskId":%q,"SnapshotId":%q}`, source.volume, source.id) || f.writes.Add(1) != 1 {
				return false
			}
			send(map[string]any{"Response": map[string]string{"RequestId": fixtureRollbackReceipt}})
		default:
			return false
		}
	case provider.AlibabaECS:
		query := r.URL.Query()
		if r.Method != http.MethodPost || r.Host != "ecs."+source.region+".aliyuncs.com" ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
			!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		switch alibabaAction {
		case "DescribeDisks":
			if query.Get("RegionId") != source.region || query.Get("DiskIds") != fmt.Sprintf("[%q]", source.volume) {
				return false
			}
			state := "Available"
			if f.attached.Load() {
				state = "In_use"
			}
			send(map[string]any{"Disks": map[string]any{"Disk": []any{map[string]any{"DiskId": source.volume,
				"RegionId": source.region, "Type": "data", "Category": "cloud_essd", "Status": state}}}})
		case "ResetDisk":
			if query.Get("DiskId") != source.volume || query.Get("SnapshotId") != source.id || len(query) != 2 || f.writes.Add(1) != 1 {
				return false
			}
			panic(http.ErrAbortHandler)
		default:
			return false
		}
	case provider.HuaweiECS:
		if r.Host != "evs."+source.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || r.URL.RawQuery != "" {
			return false
		}
		v2 := "/v2/" + fixtureHuaweiProject
		snapshotPath := v2 + "/cloudsnapshots"
		if source.class == "standard" {
			snapshotPath = "/v5/" + fixtureHuaweiProject + "/snapshots"
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == v2+"/cloudvolumes/"+source.volume:
			attachments := []any{}
			if f.attached.Load() {
				attachments = append(attachments, map[string]string{"server_id": "server-fixture"})
			}
			send(map[string]any{"volume": map[string]any{"id": source.volume, "os-vol-tenant-attr:tenant_id": fixtureHuaweiProject,
				"status": "available", "bootable": "false", "attachments": attachments}})
		case r.Method == http.MethodPost && r.URL.Path == snapshotPath+"/"+source.id+"/rollback":
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != fmt.Sprintf(`{"rollback":{"volume_id":%q}}`, source.volume) || f.writes.Add(1) != 1 {
				return false
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			if source.class == "flash" {
				send(map[string]any{"rollback": map[string]string{"volume_id": source.volume}})
			} else {
				send(map[string]string{"job_id": fixtureRollbackReceipt})
			}
		case source.class == "standard" && r.Method == http.MethodGet && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/jobs/"+fixtureRollbackReceipt:
			status, target := "RUNNING", source.volume
			switch f.jobState.Load() {
			case 1:
				status, target = "SUCCESS", "different-volume"
			case 2:
				status = "SUCCESS"
			case 3:
				send(map[string]string{"job_id": fixtureRollbackReceipt, "status": "SUCCESS"})
				return true
			}
			send(map[string]any{"job_id": fixtureRollbackReceipt, "status": status, "entities": map[string]string{"volume_id": target}})
		default:
			return false
		}
	}
	return true
}
