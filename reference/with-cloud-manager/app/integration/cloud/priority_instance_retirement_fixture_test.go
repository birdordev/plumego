package cloudtest

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const priorityRetirementReceipt = "fixture-priority-retirement-receipt"

type priorityRetirementFixture struct {
	vpcCreationFixture
	instance, disk, zone string
	unsafeDisk           atomic.Bool
	diskAbsent           atomic.Bool
	limited              atomic.Bool
	badEvidence          atomic.Bool
}

func (f *priorityRetirementFixture) serveRetirement(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var handled bool
	if f.vendor == provider.AlibabaECS {
		handled = f.serveAlibabaRetirement(t, w, r)
	} else {
		handled = f.serveHuaweiRetirement(t, w, r)
	}
	if !handled {
		t.Errorf("unexpected retirement fixture request %s %s%s action %s", r.Method, r.Host, r.URL.Path, r.Header.Get("X-ACS-Action"))
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *priorityRetirementFixture) serveAlibabaRetirement(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Host == "sts.aliyuncs.com" {
		return f.alibaba(t, w, r)
	}
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Method != http.MethodPost || r.Header.Get("X-ACS-Version") != "2014-05-26" ||
		r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	q := r.URL.Query()
	idArray := `["` + f.instance + `"]`
	diskArray := `["` + f.disk + `"]`
	switch r.Header.Get("X-ACS-Action") {
	case "DescribeInstances":
		if q.Get("RegionId") != f.region || (q.Has("InstanceIds") && q.Get("InstanceIds") != idArray) {
			return false
		}
		rows := []map[string]any{}
		if f.writes.Load() == 0 {
			rows = append(rows, map[string]any{"InstanceId": f.instance, "InstanceName": "Fixture ECS", "RegionId": f.region,
				"ZoneId": f.zone, "Status": "Stopped", "InstanceChargeType": "PostPaid",
				"DeletionProtection": false, "OperationLocks": map[string]any{"LockReason": []any{}}})
		}
		testsupport.WriteJSON(t, w, map[string]any{"Instances": map[string]any{"Instance": rows}})
	case "DescribeDisks":
		if q.Get("RegionId") != f.region || (q.Has("InstanceId") && q.Get("InstanceId") != f.instance) ||
			(q.Has("DiskIds") && q.Get("DiskIds") != diskArray) || (q.Has("InstanceId") && q.Has("DiskIds")) {
			return false
		}
		if f.writes.Load() > 0 && (f.limited.Load() || !q.Has("DiskIds")) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		rows := []map[string]any{}
		if f.writes.Load() == 0 || !f.diskAbsent.Load() {
			rows = append(rows, map[string]any{"DiskId": f.disk, "RegionId": f.region, "ZoneId": f.zone, "Status": "In_use",
				"InstanceId": f.instance, "Type": "system", "Category": "cloud_essd", "MultiAttach": "Disabled",
				"DeleteWithInstance": true, "DeleteAutoSnapshot": f.unsafeDisk.Load()})
		}
		response := map[string]any{"Disks": map[string]any{"Disk": rows}}
		if q.Has("DiskIds") && f.badEvidence.Load() {
			response["NextToken"] = "partial-page"
		}
		testsupport.WriteJSON(t, w, response)
	case "DeleteInstance":
		if q.Get("InstanceId") != f.instance || q.Get("Force") != "false" || q.Get("TerminateSubscription") != "false" || f.unsafeDisk.Load() {
			return false
		}
		f.writes.Add(1)
		testsupport.WriteJSON(t, w, map[string]string{"RequestId": priorityRetirementReceipt})
	default:
		return false
	}
	return true
}

func (f *priorityRetirementFixture) serveHuaweiRetirement(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Host == "iam.myhuaweicloud.com" {
		return f.huawei(t, w, r)
	}
	if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || r.Header.Get("X-Subject-Token") != "" {
		return false
	}
	ecs := r.Host == "ecs."+f.region+".myhuaweicloud.com"
	evs := r.Host == "evs."+f.region+".myhuaweicloud.com"
	project := "/" + fixtureHuaweiProject
	base := "/v1" + project + "/cloudservers"
	volumeBase := "/v2" + project + "/cloudvolumes/"
	const device = "/dev/vda"
	server := map[string]any{
		"id": f.instance, "name": "Fixture ECS", "status": "SHUTOFF", "tenant_id": fixtureHuaweiProject,
		"locked": false, "OS-EXT-AZ:availability_zone": f.zone, "OS-EXT-SRV-ATTR:root_device_name": device,
		"metadata": map[string]string{"charging_mode": "0"},
		"os-extended-volumes:volumes_attached": []map[string]any{{
			"id": f.disk, "device": device, "bootIndex": "0", "delete_on_termination": !f.unsafeDisk.Load(),
		}},
	}
	volume := map[string]any{
		"id": f.disk, "status": "in-use", "bootable": "true", "multiattach": false, "availability_zone": f.zone,
		"os-vol-tenant-attr:tenant_id": fixtureHuaweiProject,
		"attachments":                  []map[string]string{{"server_id": f.instance, "volume_id": f.disk}},
	}
	path := r.URL.Path
	switch {
	case ecs && r.Method == http.MethodGet && path == "/v1.1"+project+"/cloudservers/detail" && r.URL.Query().Has("limit"):
		testsupport.WriteJSON(t, w, map[string]any{"servers": []any{server}})
	case evs && r.Method == http.MethodGet && path == volumeBase+"detail" && r.URL.Query().Has("limit"):
		testsupport.WriteJSON(t, w, map[string]any{"volumes": []any{volume}})
	case ecs && r.Method == http.MethodGet && r.URL.RawQuery == "" && path == base+"/"+f.instance:
		if f.writes.Load() == 0 {
			testsupport.WriteJSON(t, w, map[string]any{"server": server})
		} else {
			w.WriteHeader(http.StatusNotFound)
			testsupport.WriteJSON(t, w, map[string]any{"error": map[string]string{"code": "Ecs.0114"}})
		}
	case ecs && r.Method == http.MethodGet && r.URL.RawQuery == "" && path == base+"/"+f.instance+"/os-volume_attachments":
		testsupport.WriteJSON(t, w, map[string]any{"volumeAttachments": []map[string]string{{
			"id": f.disk, "serverId": f.instance, "volumeId": f.disk, "device": device,
		}}})
	case evs && r.Method == http.MethodGet && r.URL.RawQuery == "" && path == volumeBase+f.disk:
		if f.writes.Load() > 0 && f.limited.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
		} else if f.diskAbsent.Load() {
			code := "EVS.5404"
			if f.badEvidence.Load() {
				code = "unrelated-not-found"
			}
			w.WriteHeader(http.StatusNotFound)
			testsupport.WriteJSON(t, w, map[string]string{"error_code": code})
		} else {
			testsupport.WriteJSON(t, w, map[string]any{"volume": volume})
		}
	case ecs && r.Method == http.MethodPost && path == base+"/delete":
		var body struct {
			Servers      []struct{ ID string }
			DeleteVolume *bool `json:"delete_volume"`
			DeletePublic *bool `json:"delete_publicip"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || len(body.Servers) != 1 || body.Servers[0].ID != f.instance ||
			body.DeleteVolume == nil || *body.DeleteVolume || body.DeletePublic == nil || *body.DeletePublic || f.unsafeDisk.Load() {
			return false
		}
		f.writes.Add(1)
		w.WriteHeader(http.StatusAccepted)
		testsupport.WriteJSON(t, w, map[string]string{"job_id": priorityRetirementReceipt})
	case ecs && r.Method == http.MethodGet && r.URL.RawQuery == "" && path == "/v1"+project+"/jobs/"+priorityRetirementReceipt:
		testsupport.WriteJSON(t, w, map[string]any{"job_id": priorityRetirementReceipt, "job_type": "deleteVMs", "status": "SUCCESS",
			"project_id": fixtureHuaweiProject, "entities": map[string]any{"sub_jobs_total": 1, "sub_jobs": []any{
				map[string]any{"job_type": "deleteVM", "status": "SUCCESS", "entities": map[string]string{"server_id": f.instance}},
			}}})
	default:
		return false
	}
	return true
}
