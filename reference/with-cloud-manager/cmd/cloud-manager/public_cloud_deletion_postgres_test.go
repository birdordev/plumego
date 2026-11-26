package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

const fixtureDeletionReceipt = "fixture-delete-receipt"

const (
	deletionFixtureOff int32 = iota
	deletionFixtureUnsafe
	deletionFixtureIncomplete
	deletionFixtureLost
	deletionFixtureReady
)

type deletionFixture struct {
	deletionMode   atomic.Int32
	deletionWrites atomic.Int32
	deletionDone   atomic.Bool
}

func (f *powerFixture) exerciseDeletion(t *testing.T, db *sql.DB, store operations.Store, app http.Handler, scope identity.Scope, actor, reviewer string, enable *atomic.Bool) {
	t.Helper()
	ctx := t.Context()
	query := url.Values{"account": {f.provider}, "region": {f.region}, "asset_id": {f.instance}}
	request := httptest.NewRequest(http.MethodGet, operationsRouteForFixture+"/deletion-preview?"+query.Encode(), nil)
	request.Header.Set("Authorization", "Bearer "+actor)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	var preview struct{ Data operations.DeletionPreview }
	supported := f.provider != provider.TencentCVM
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || response.Code != http.StatusOK ||
		preview.Data.ProviderSupported != supported || !preview.Data.ApprovalRequired ||
		preview.Data.AssetID != f.instance || preview.Data.OwnershipRevision != 1 {
		t.Fatalf("deletion preview: status %d, body %s, error %v", response.Code, response.Body, err)
	}
	submit := func(key string, code int) operations.Operation {
		t.Helper()
		return submitFixturePower(t, app, f.provider, f.region, f.instance, actor, "delete", f.provider+"-delete-"+key, code)
	}
	if !supported {
		submit("unsupported", http.StatusBadRequest)
		return
	}
	checkAsset := func(removed bool) {
		t.Helper()
		var actual bool
		err := db.QueryRowContext(ctx, `SELECT o.removed_at IS NOT NULL
			FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
			WHERE s.account_id=$1 AND o.external_id=$2 AND s.kind='instance'`, f.provider, f.instance).Scan(&actual)
		if err != nil || actual != removed {
			t.Fatalf("asset removed %t, want %t: %v", actual, removed, err)
		}
	}
	wait := func(id, state string, advance bool) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, id, state, advance)
	}
	approve := func(op operations.Operation, writes int32) {
		t.Helper()
		if op.Status != "awaiting_approval" || f.deletionWrites.Load() != writes {
			t.Fatalf("delete was not held for approval: %+v", op)
		}
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}

	disabled := submit("disabled", http.StatusAccepted)
	approve(disabled, 0)
	if got := wait(disabled.ID, "failed", false); got.OutcomeReason != "provider_deletion_disabled" {
		t.Fatalf("default deletion guard: %+v", got)
	}
	enable.Store(true)
	for _, scenario := range []int32{deletionFixtureUnsafe, deletionFixtureIncomplete} {
		f.deletionMode.Store(scenario)
		op := submit(fmt.Sprintf("preflight-%d", scenario), http.StatusAccepted)
		approve(op, 0)
		if got := wait(op.ID, "failed", false); got.OutcomeReason != "deletion_preconditions_unconfirmed" || f.deletionWrites.Load() != 0 {
			t.Fatalf("unsafe or incomplete evidence reached dispatch: %+v", got)
		}
		checkAsset(false)
	}

	f.deletionMode.Store(deletionFixtureLost)
	lost := submit("lost-response", http.StatusAccepted)
	approve(lost, 0)
	review := wait(lost.ID, "needs_review", true)
	if review.OutcomeReason != "deletion_completion_requires_evidence" || review.ProviderRequestID != "" || f.deletionWrites.Load() != 1 {
		t.Fatalf("unconfirmed deletion: %+v", review)
	}
	checkAsset(false)
	if _, err := store.Resolve(ctx, reviewer, scope, lost.ID, operations.Resolution{
		Decision: "failed", EvidenceRef: "fixture/remote-instance-retained",
	}); err != nil {
		t.Fatal(err)
	}
	checkAsset(false)

	f.deletionMode.Store(deletionFixtureReady)
	operation := submit("retained-disks", http.StatusAccepted)
	approve(operation, 1)
	if f.provider == provider.HuaweiECS {
		got := wait(operation.ID, "observing", false)
		if got.ProviderRequestID != fixtureDeletionReceipt {
			t.Fatalf("missing Huawei job receipt: %+v", got)
		}
		waitPendingCloudObservation(t, db, operation.ID)
		checkAsset(false)
		f.deletionDone.Store(true)
		final := wait(operation.ID, "succeeded", true)
		if final.OutcomeReason != "provider_deletion_confirmed" {
			t.Fatalf("Huawei job not confirmed: %+v", final)
		}
		checkAsset(true)
	} else {
		got := wait(operation.ID, "needs_review", false)
		if got.ProviderRequestID != fixtureDeletionReceipt || got.OutcomeReason != "deletion_completion_requires_evidence" {
			t.Fatalf("unconfirmed Alibaba receipt: %+v", got)
		}
		resolution := operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/remote-release", ProviderRequestID: "foreign"}
		if _, err := store.Resolve(ctx, reviewer, scope, operation.ID, resolution); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("accepted mismatched receipt: %v", err)
		}
		resolution.ProviderRequestID = fixtureDeletionReceipt
		if _, err := store.Resolve(ctx, reviewer, scope, operation.ID, resolution); err != nil {
			t.Fatal(err)
		}
		checkAsset(false)
	}
	if f.deletionWrites.Load() != 2 {
		t.Fatalf("delete was sent %d times across two dispatched operations", f.deletionWrites.Load())
	}
}

const operationsRouteForFixture = "/v1/tenants/tenant/projects/project/instance-operations"

func (f *powerFixture) serveDeletion(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	mode := f.deletionMode.Load()
	if mode == deletionFixtureOff {
		return false
	}
	if f.provider == provider.AlibabaECS && r.Host == "ecs."+f.region+".aliyuncs.com" {
		f.serveAlibabaDeletion(t, w, r, mode)
		return true
	}
	if f.provider == provider.HuaweiECS && r.Host == "ecs."+f.region+".myhuaweicloud.com" {
		f.serveHuaweiDeletion(t, w, r, mode)
		return true
	}
	return false
}

func (f *powerFixture) serveAlibabaDeletion(t *testing.T, w http.ResponseWriter, r *http.Request, mode int32) {
	t.Helper()
	q := r.URL.Query()
	action := r.Header.Get("X-ACS-Action")
	if r.Method != http.MethodPost || !validFixtureAlibabaSignature(r, f.key, f.secret) ||
		r.Header.Get("X-ACS-Security-Token") != f.session || r.Header.Get("X-ACS-Version") != "2014-05-26" {
		t.Error("invalid Alibaba deletion credentials or method")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	const zone = "cn-hangzhou-a"
	switch {
	case action == "DescribeInstances" && q.Get("InstanceIds") == fmt.Sprintf(`[%q]`, f.instance) && q.Get("RegionId") == f.region:
		writeCloudFixtureJSON(t, w, map[string]any{"Instances": map[string]any{"Instance": []any{map[string]any{
			"InstanceId": f.instance, "RegionId": f.region, "ZoneId": zone, "Status": "Stopped", "InstanceChargeType": "PostPaid",
			"OperationLocks": map[string]any{"LockReason": []any{}},
		}}}})
	case action == "DescribeDisks" && q.Get("InstanceId") == f.instance && q.Get("RegionId") == f.region &&
		q.Get("MaxResults") == "100" && !q.Has("DiskIds"):
		disk := func(id, kind string, release bool) map[string]any {
			return map[string]any{"DiskId": id, "RegionId": f.region, "ZoneId": zone, "Type": kind, "Category": "cloud_essd",
				"Status": "In_use", "InstanceId": f.instance, "MultiAttach": "Disabled", "DeleteWithInstance": release}
		}
		result := map[string]any{"Disks": map[string]any{"Disk": []any{
			disk("d-root", "system", false), disk(f.volumeID(), "data", mode == deletionFixtureUnsafe),
		}}}
		if mode == deletionFixtureIncomplete {
			result["NextToken"] = "next-page"
		}
		writeCloudFixtureJSON(t, w, result)
	case action == "DeleteInstance" && q.Get("InstanceId") == f.instance && q.Get("Force") == "false" &&
		q.Get("TerminateSubscription") == "false":
		f.deletionWrites.Add(1)
		if mode == deletionFixtureLost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeCloudFixtureJSON(t, w, map[string]string{"RequestId": fixtureDeletionReceipt})
	default:
		t.Errorf("unexpected Alibaba deletion API %q", action)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *powerFixture) serveHuaweiDeletion(t *testing.T, w http.ResponseWriter, r *http.Request, mode int32) {
	t.Helper()
	if r.Header.Get("X-Auth-Token") != f.token {
		t.Error("invalid Huawei deletion token")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	base := "/v1/" + f.project
	instance := base + "/cloudservers/" + f.instance
	const device = "/dev/vdb"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == instance:
		boot := "-1"
		if mode == deletionFixtureUnsafe {
			boot = "0"
		}
		writeCloudFixtureJSON(t, w, map[string]any{"server": map[string]any{
			"id": f.instance, "tenant_id": f.project, "status": "SHUTOFF", "locked": false,
			"OS-EXT-SRV-ATTR:root_device_name": "/dev/vda",
			"os-extended-volumes:volumes_attached": []any{map[string]any{
				"id": f.volumeID(), "device": device, "bootIndex": boot, "delete_on_termination": "false",
			}},
		}})
	case r.Method == http.MethodGet && r.URL.Path == instance+"/os-volume_attachments":
		rows := []any{map[string]string{"id": f.volumeID(), "volumeId": f.volumeID(), "serverId": f.instance, "device": device}}
		if mode == deletionFixtureIncomplete {
			rows = []any{}
		}
		writeCloudFixtureJSON(t, w, map[string]any{"volumeAttachments": rows})
	case r.Method == http.MethodPost && r.URL.Path == base+"/cloudservers/delete":
		var payload struct {
			Servers        []struct{ ID string }
			DeleteVolume   *bool `json:"delete_volume"`
			DeletePublicIP *bool `json:"delete_publicip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Servers) != 1 ||
			payload.Servers[0].ID != f.instance || payload.DeleteVolume == nil || *payload.DeleteVolume ||
			payload.DeletePublicIP == nil || *payload.DeletePublicIP || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("invalid Huawei deletion request: %+v %v", payload, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.deletionWrites.Add(1)
		if mode == deletionFixtureLost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		writeCloudFixtureJSON(t, w, map[string]string{"job_id": fixtureDeletionReceipt})
	case r.Method == http.MethodGet && r.URL.Path == base+"/jobs/"+fixtureDeletionReceipt:
		job := map[string]any{"job_id": fixtureDeletionReceipt, "job_type": "deleteVMs", "status": "RUNNING"}
		if f.deletionDone.Load() {
			job["status"] = "SUCCESS"
			job["entities"] = map[string]any{"sub_jobs_total": 1, "sub_jobs": []any{map[string]any{
				"job_type": "deleteVM", "status": "SUCCESS", "entities": map[string]string{"server_id": f.instance},
			}}}
		}
		writeCloudFixtureJSON(t, w, job)
	default:
		t.Errorf("unexpected Huawei deletion API %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func writeCloudFixtureJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
