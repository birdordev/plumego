package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

type volumeMutationFixture struct {
	volumeAttached, volumeRoot, heldAttachment atomic.Bool
	volumeWrites                               atomic.Int32
}

func (f *powerFixture) volumeID() string {
	switch f.provider {
	case provider.TencentCVM:
		return "disk-volume"
	case provider.AlibabaECS:
		return "d-volume"
	default:
		return "40000000-0000-4000-8000-000000000001"
	}
}

func (f *powerFixture) exerciseVolumeChanges(t *testing.T, db *sql.DB, store operations.Store, app http.Handler, scope identity.Scope, actor, reviewer string) {
	t.Helper()
	ctx := t.Context()
	const receipt = "fixture-volume-receipt"
	if _, err := db.ExecContext(ctx, `WITH s AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'volume',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
	SELECT scope_id,$3,'fixture-volume','available',1 FROM s`, f.provider, f.region, f.volumeID()); err != nil {
		t.Fatal(err)
	}
	submit := func(action, key string) operations.Operation {
		t.Helper()
		input := operations.Input{Account: f.provider, Region: f.region, AssetID: f.instance, Action: action,
			VolumeID: f.volumeID(), VolumeRevision: 1, GuestUnmounted: action == "detach-volume"}
		if action == "attach-volume" && f.provider == provider.HuaweiECS {
			input.Device = "/dev/sdf"
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/instance-operations", strings.NewReader(string(body)))
		request.Header.Set("Authorization", "Bearer "+actor)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", f.provider+"-"+key)
		request.Header.Set("If-Match", `"1"`)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, request)
		var payload struct{ Data operations.Operation }
		if err := json.Unmarshal(w.Body.Bytes(), &payload); w.Code != http.StatusAccepted || err != nil || payload.Data.ID == "" {
			t.Fatalf("submit %s: status %d, body %s, error %v", action, w.Code, w.Body.String(), err)
		}
		return payload.Data
	}
	wait := func(id, state string, advance bool) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, id, state, advance)
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if op.Status != "awaiting_approval" {
			t.Fatalf("missing detach approval: %+v", op)
		}
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	projected := func(want bool) {
		t.Helper()
		var volumeToInstance, instanceToVolume bool
		err := db.QueryRowContext(ctx, `SELECT
			count(*) FILTER (WHERE s.kind='volume' AND o.external_id=$2 AND o.status='in-use' AND o.links @>
				jsonb_build_array(jsonb_build_object('kind','instance','id',$3::text,'relation','attached_to'))) = 1,
			count(*) FILTER (WHERE s.kind='instance' AND o.external_id=$3 AND o.links @>
				jsonb_build_array(jsonb_build_object('kind','volume','id',$2::text,'relation','uses_volume'))) = 1
			FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
			WHERE s.account_id=$1`, f.provider, f.volumeID(), f.instance).Scan(&volumeToInstance, &instanceToVolume)
		if err != nil || volumeToInstance != want || instanceToVolume != want {
			t.Fatalf("volume topology state %t/%t, want %t: %v", volumeToInstance, instanceToVolume, want, err)
		}
	}

	attach := submit("attach-volume", "attach")
	if observed := wait(attach.ID, "observing", false); observed.ProviderRequestID != receipt {
		t.Fatalf("attach receipt lost: %+v", observed)
	}
	wait(attach.ID, "succeeded", true)
	projected(true)

	f.volumeRoot.Store(true)
	blocked := submit("detach-volume", "root-detach")
	approve(blocked)
	if failed := wait(blocked.ID, "failed", false); failed.OutcomeReason != "provider_volume_precondition" ||
		f.volumeWrites.Load() != 1 {
		t.Fatalf("root disk did not block detach: %+v", failed)
	}

	f.volumeRoot.Store(false)
	detach := submit("detach-volume", "detach")
	if f.provider == provider.HuaweiECS {
		f.heldAttachment.Store(true)
	}
	approve(detach)
	if observed := wait(detach.ID, "observing", false); observed.OutcomeReason != "submission_outcome_unknown" {
		t.Fatalf("lost detach response was not observed: %+v", observed)
	}
	if f.provider == provider.HuaweiECS {
		waitPendingCloudObservation(t, db, detach.ID)
		f.heldAttachment.Store(false)
	}
	wait(detach.ID, "succeeded", true)
	if f.volumeWrites.Load() != 2 || f.volumeAttached.Load() {
		t.Fatalf("volume mutations repeated, writes %d", f.volumeWrites.Load())
	}
	projected(false)
}

func (f *powerFixture) serveVolumeMutation(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	bad := func() bool {
		t.Errorf("incorrect volume mutation request, host %s path %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	const receipt = "fixture-volume-receipt"
	var response any
	var attaching *bool
	switch r.Host {
	case "cbs.tencentcloudapi.com":
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Version") != "2017-03-12" {
			return bad()
		}
		var target struct {
			InstanceId         string
			DiskIds            []string
			DeleteWithInstance *bool
		}
		if json.Unmarshal(body, &target) != nil || !slices.Equal(target.DiskIds, []string{f.volumeID()}) ||
			target.DeleteWithInstance != nil {
			return bad()
		}
		switch action := r.Header.Get("X-TC-Action"); action {
		case "DescribeDisks":
			state, host, usage := "UNATTACHED", "", "DATA_DISK"
			if f.volumeAttached.Load() {
				state, host = "ATTACHED", f.instance
			}
			if f.volumeRoot.Load() {
				usage = "SYSTEM_DISK"
			}
			response = map[string]any{"Response": map[string]any{"TotalCount": 1, "DiskSet": []any{map[string]any{
				"DiskId": f.volumeID(), "DiskState": state, "DiskUsage": usage, "InstanceId": host,
				"Placement": map[string]string{"Zone": f.region + "-1"}, "Portable": true, "Shareable": false, "DeleteWithInstance": false,
			}}}}
		case "AttachDisks", "DetachDisks":
			if target.InstanceId != f.instance {
				return bad()
			}
			value := action == "AttachDisks"
			attaching = &value
			response = map[string]any{"Response": map[string]string{"RequestId": receipt}}
		default:
			return bad()
		}
	case "ecs.cn-hangzhou.aliyuncs.com":
		action := r.Header.Get("X-ACS-Action")
		if action != "DescribeDisks" && action != "AttachDisk" && action != "DetachDisk" {
			return false
		}
		if !validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Security-Token") != f.session ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" {
			return bad()
		}
		q := r.URL.Query()
		if action == "DescribeDisks" {
			if q.Get("DiskIds") != fmt.Sprintf(`[%q]`, f.volumeID()) || q.Get("RegionId") != f.region {
				return bad()
			}
			state, host, usage := "Available", "", "data"
			if f.volumeAttached.Load() {
				state, host = "In_use", f.instance
			}
			if f.volumeRoot.Load() {
				usage = "system"
			}
			response = map[string]any{"Disks": map[string]any{"Disk": []any{map[string]any{
				"DiskId": f.volumeID(), "Status": state, "Type": usage, "InstanceId": host, "RegionId": f.region,
				"ZoneId": f.region + "-a", "Category": "cloud_essd", "Portable": true, "MultiAttach": "Disabled", "DeleteWithInstance": false,
			}}}}
		} else {
			if q.Get("DiskId") != f.volumeID() || q.Get("InstanceId") != f.instance || q.Get("DeleteWithInstance") != "false" ||
				(action == "AttachDisk" && q.Get("Bootable") != "false") {
				return bad()
			}
			value := action == "AttachDisk"
			attaching = &value
			response = map[string]string{"RequestId": receipt}
		}
	case "evs.cn-north-4.myhuaweicloud.com":
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != f.token ||
			r.URL.Path != "/v2/"+f.project+"/cloudvolumes/"+f.volumeID() {
			return bad()
		}
		state, boot := "available", "false"
		rows := []any{}
		if f.volumeAttached.Load() {
			state = "in-use"
			rows = append(rows, map[string]string{"server_id": f.instance, "volume_id": f.volumeID()})
		}
		if f.volumeRoot.Load() {
			boot = "true"
		}
		response = map[string]any{"volume": map[string]any{
			"id": f.volumeID(), "status": state, "bootable": boot, "multiattach": false, "availability_zone": f.region + "a",
			"os-vol-tenant-attr:tenant_id": f.project, "attachments": rows,
		}}
	case "ecs.cn-north-4.myhuaweicloud.com":
		base := "/v1/" + f.project + "/cloudservers/" + f.instance
		value := r.URL.Path == base+"/attachvolume"
		if !value && r.URL.Path != base+"/detachvolume/"+f.volumeID() {
			return false
		}
		if r.Header.Get("X-Auth-Token") != f.token {
			return bad()
		}
		if value {
			var input struct {
				VolumeAttachment struct{ VolumeId, Device string }
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil ||
				input.VolumeAttachment.VolumeId != f.volumeID() || input.VolumeAttachment.Device != "/dev/sdf" {
				return bad()
			}
		} else if r.Method != http.MethodDelete || r.URL.Query().Get("delete_flag") != "0" {
			return bad()
		}
		attaching = &value
		response = map[string]string{"job_id": receipt}
	default:
		return false
	}
	if attaching != nil {
		f.volumeWrites.Add(1)
		f.volumeAttached.Store(*attaching)
		if !*attaching {
			interruptFixtureResponse(t, w)
			return true
		}
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
	return true
}

func interruptFixtureResponse(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
}
