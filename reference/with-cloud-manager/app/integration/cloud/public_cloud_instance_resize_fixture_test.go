package cloudtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const instanceResizeReceipt = "fixture-instance-resize"

type instanceResizeFixture struct {
	vpcCreationFixture
	id, original, target string
	sends, jobReads      atomic.Int32
	enabled, prepaid     atomic.Bool
	written, pending     atomic.Bool
	loseReply, wrongRead atomic.Bool
	optionsUnavailable   atomic.Bool
}

func (f *instanceResizeFixture) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.serve(t, w, r)
		return
	}
	handled := false
	switch f.vendor {
	case provider.TencentCVM:
		handled = f.serveTencentResize(t, w, r)
	case provider.AlibabaECS:
		handled = f.serveAlibabaResize(t, w, r)
	case provider.HuaweiECS:
		handled = f.serveHuaweiResize(t, w, r)
	}
	if !handled {
		t.Errorf("unexpected compute resize request %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *instanceResizeFixture) serveTencentResize(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "cvm.tencentcloudapi.com" || r.Method != http.MethodPost || r.URL.RequestURI() != "/" ||
		r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var req struct {
		InstanceIds  []string
		InstanceType string
		ForceStop    *bool
		Filters      []struct {
			Name   string
			Values []string
		}
		Limit int
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	exact := len(req.InstanceIds) == 1 && req.InstanceIds[0] == f.id
	switch r.Header.Get("X-TC-Action") {
	case "DescribeInstances":
		if exact || (len(req.InstanceIds) == 0 && req.Limit == 100) {
			charge, operation, receipt, operationState := "POSTPAID_BY_HOUR", "StopInstances", "fixture-stop", "SUCCESS"
			if f.prepaid.Load() {
				charge = "PREPAID"
			}
			if f.written.Load() {
				operation, receipt = "ResetInstancesType", instanceResizeReceipt
				if f.pending.Load() {
					operationState = "OPERATING"
				}
			}
			row := map[string]any{"InstanceId": f.id, "InstanceName": "Fixture instance", "InstanceType": f.observedType(),
				"InstanceState": f.observedState(), "InstanceChargeType": charge,
				"Placement": map[string]string{"Zone": f.region + "-1"}, "SystemDisk": map[string]string{"DiskType": "CLOUD_SSD"},
				"LatestOperation": operation, "LatestOperationRequestId": receipt, "LatestOperationState": operationState}
			testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 1, "InstanceSet": []any{row}}})
			return true
		}
	case "DescribeInstancesModification":
		if exact && len(req.Filters) == 1 && req.Filters[0].Name == "status" &&
			len(req.Filters[0].Values) == 1 && req.Filters[0].Values[0] == "SELL" {
			zone := f.region + "-1"
			if f.optionsUnavailable.Load() {
				zone = f.region + "-2"
			}
			testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 1,
				"InstanceTypeConfigStatusSet": []any{map[string]any{"Status": "SELL",
					"InstanceTypeConfig": map[string]string{"Zone": zone, "InstanceType": f.target}}}}})
			return true
		}
	case "ResetInstancesType":
		if exact && req.InstanceType == f.target && req.ForceStop != nil && !*req.ForceStop {
			f.recordWrite()
			testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]string{"RequestId": instanceResizeReceipt}})
			return true
		}
	}
	return false
}

func (f *instanceResizeFixture) serveAlibabaResize(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Method != http.MethodPost || r.URL.Path != "/" ||
		r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	query := r.URL.Query()
	switch r.Header.Get("X-ACS-Action") {
	case "DescribeInstances":
		ids := query.Get("InstanceIds")
		if query.Get("RegionId") == f.region && (ids == "" || ids == `["`+f.id+`"]`) {
			charge := "PostPaid"
			if f.prepaid.Load() {
				charge = "PrePaid"
			}
			row := map[string]any{"InstanceId": f.id, "InstanceName": "Fixture instance", "InstanceType": f.observedType(),
				"Status": f.observedState(), "InstanceChargeType": charge, "RegionId": f.region, "ZoneId": f.region + "-a",
				"SpotStrategy": "NoSpot", "StoppedMode": "KeepCharging", "OperationLocks": map[string]any{"LockReason": []any{}}}
			testsupport.WriteJSON(t, w, map[string]any{"Instances": map[string]any{"Instance": []any{row}}})
			return true
		}
	case "DescribeResourcesModification":
		if query.Get("ResourceId") == f.id && query.Get("RegionId") == f.region &&
			query.Get("DestinationResource") == "InstanceType" && query.Get("OperationType") == "Upgrade" &&
			query.Get("MigrateAcrossZone") == "false" {
			status := "Available"
			if f.optionsUnavailable.Load() {
				status = "SoldOut"
			}
			resource := map[string]any{"Type": "InstanceType", "SupportedResources": map[string]any{
				"SupportedResource": []any{map[string]string{"Status": status, "Value": f.target}},
			}}
			zone := map[string]any{"ZoneId": f.region + "-a", "RegionId": f.region, "Status": "Available",
				"AvailableResources": map[string]any{"AvailableResource": []any{resource}}}
			testsupport.WriteJSON(t, w, map[string]any{"AvailableZones": map[string]any{"AvailableZone": []any{zone}}})
			return true
		}
	case "ModifyInstanceSpec":
		if query.Get("InstanceId") == f.id && query.Get("InstanceType") == f.target &&
			query.Get("Async") == "false" && query.Get("AllowMigrateAcrossZone") == "false" && len(query) == 4 {
			f.recordWrite()
			testsupport.WriteJSON(t, w, map[string]string{"RequestId": instanceResizeReceipt})
			return true
		}
	}
	return false
}

func (f *instanceResizeFixture) serveHuaweiResize(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Host != "ecs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	instance := "/v1/" + fixtureHuaweiProject + "/cloudservers/" + f.id
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == instance ||
		(r.URL.Path == "/v1.1/"+fixtureHuaweiProject+"/cloudservers/detail" && r.URL.Query().Get("limit") == "100")):
		charge, task := "0", ""
		if f.prepaid.Load() {
			charge = "1"
		}
		if f.written.Load() && f.pending.Load() {
			task = "resize"
		}
		row := map[string]any{"id": f.id, "name": "Fixture instance", "status": f.observedState(), "tenant_id": fixtureHuaweiProject,
			"flavor": map[string]string{"id": f.observedType()}, "locked": false, "OS-EXT-STS:task_state": task,
			"OS-EXT-AZ:availability_zone": f.region + "a", "availability_zone": f.region + "a",
			"metadata": map[string]string{"charging_mode": charge}}
		if r.URL.Path == instance {
			testsupport.WriteJSON(t, w, map[string]any{"server": row})
		} else {
			testsupport.WriteJSON(t, w, map[string]any{"servers": []any{row}})
		}
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/cloudservers/resize_flavors":
		query := r.URL.Query()
		if query.Get("instance_uuid") != f.id || query.Get("limit") != "100" || query.Get("sort_key") != "flavorid" || query.Get("sort_dir") != "asc" {
			return false
		}
		rows := make([]any, 0, 100)
		switch query.Get("marker") {
		case "":
			for i := range 100 {
				rows = append(rows, map[string]string{"id": fmt.Sprintf("a-%03d", i)})
			}
		case "a-099":
			rows = append(rows, map[string]any{"id": f.target, "OS-FLV-DISABLED:disabled": f.optionsUnavailable.Load()})
		default:
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"flavors": rows})
		return true
	case r.Method == http.MethodGet && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/jobs/"+instanceResizeReceipt:
		state := "SUCCESS"
		if f.pending.Load() {
			state = "RUNNING"
		}
		f.jobReads.Add(1)
		testsupport.WriteJSON(t, w, map[string]any{"job_id": instanceResizeReceipt, "status": state,
			"entities": map[string]any{"sub_jobs_total": 1, "sub_jobs": []any{map[string]any{
				"status": state, "job_type": "resizeServer", "entities": map[string]string{"server_id": f.id},
			}}}})
		return true
	case r.Method == http.MethodPost && r.URL.Path == instance+"/resize":
		var body map[string]map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || len(body["resize"]) != 1 ||
			body["resize"]["flavorRef"] != f.target || r.Header.Get("Content-Type") != "application/json" || r.URL.RawQuery != "" {
			return false
		}
		f.recordWrite()
		w.WriteHeader(http.StatusAccepted)
		testsupport.WriteJSON(t, w, map[string]string{"job_id": instanceResizeReceipt})
		return true
	}
	return false
}

func (f *instanceResizeFixture) recordWrite() {
	f.sends.Add(1)
	f.written.Store(true)
	if f.loseReply.Load() {
		f.wrongRead.Store(true)
		panic(http.ErrAbortHandler)
	}
}

func (f *instanceResizeFixture) observedType() string {
	if f.wrongRead.Load() {
		return "wrong.fixture.spec"
	}
	if f.written.Load() && (!f.pending.Load() || f.vendor == provider.HuaweiECS) {
		return f.target
	}
	return f.original
}

func (f *instanceResizeFixture) observedState() string {
	if f.vendor == provider.TencentCVM && f.written.Load() && !f.pending.Load() {
		return "RUNNING"
	}
	return provider.InstanceStoppedState(f.vendor)
}
