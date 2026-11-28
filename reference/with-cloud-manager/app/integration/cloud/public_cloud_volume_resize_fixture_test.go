package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const volumeResizeReceipt = "fixture-volume-resize"

type volumeResizeFixture struct {
	vpcCreationFixture
	id, snapshot, available, resizing                      string
	size, sendCount, jobReads                              atomic.Int32
	holdAfterWrite, pending, loseReply, wrongRead, prepaid atomic.Bool
}

func (f *volumeResizeFixture) serveResize(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.serve(t, w, r)
		return
	}
	fail := func() {
		t.Errorf("unexpected resize request %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
	var target int32
	switch f.vendor {
	case provider.TencentCVM:
		body, err := io.ReadAll(r.Body)
		var request struct {
			DiskId   string
			DiskIds  []string
			DiskSize int32
		}
		if err != nil || json.Unmarshal(body, &request) != nil ||
			r.Host != "cbs.tencentcloudapi.com" || r.Method != http.MethodPost || r.URL.RequestURI() != "/" ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Version") != "2017-03-12" ||
			r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
			fail()
			return
		}
		switch r.Header.Get("X-TC-Action") {
		case "DescribeDisks":
			if len(request.DiskIds) == 1 && request.DiskIds[0] == f.id {
				testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 1, "DiskSet": []any{f.diskRow()}}})
				return
			}
		case "ResizeDisk":
			if request.DiskId == f.id && len(request.DiskIds) == 0 {
				target = request.DiskSize
			}
		}
	case provider.AlibabaECS:
		query := r.URL.Query()
		if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Method != http.MethodPost || r.URL.Path != "/" ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" ||
			r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
			fail()
			return
		}
		switch r.Header.Get("X-ACS-Action") {
		case "DescribeDisks":
			if query.Get("DiskIds") == `["`+f.id+`"]` && query.Get("RegionId") == f.region {
				testsupport.WriteJSON(t, w, map[string]any{"Disks": map[string]any{"Disk": []any{f.diskRow()}}})
				return
			}
		case "ResizeDisk":
			if query.Get("DiskId") == f.id && query.Get("Type") == "offline" && len(query) == 3 {
				if parsed, err := strconv.ParseInt(query.Get("NewSize"), 10, 32); err == nil {
					target = int32(parsed)
				}
			}
		}
	case provider.HuaweiECS:
		if r.Host != "evs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || r.URL.RawQuery != "" {
			fail()
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/"+fixtureHuaweiProject+"/cloudvolumes/"+f.id:
			testsupport.WriteJSON(t, w, map[string]any{"volume": f.diskRow()})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/jobs/"+volumeResizeReceipt:
			f.jobReads.Add(1)
			state := "SUCCESS"
			if f.pending.Load() {
				state = "RUNNING"
			}
			testsupport.WriteJSON(t, w, map[string]any{"job_id": volumeResizeReceipt, "job_type": "extendVolume", "status": state,
				"entities": map[string]any{"volume_id": f.id, "size": f.size.Load()}})
			return
		case r.Method == http.MethodPost && r.URL.Path == "/v2.1/"+fixtureHuaweiProject+"/cloudvolumes/"+f.id+"/action":
			var body map[string]struct {
				Size int32 `json:"new_size"`
			}
			if json.NewDecoder(r.Body).Decode(&body) == nil && len(body) == 1 && r.Header.Get("Content-Type") == "application/json" {
				target = body["os-extend"].Size
			}
		}
	}
	if target <= f.size.Load() {
		fail()
		return
	}
	f.sendCount.Add(1)
	f.size.Store(target)
	f.pending.Store(f.holdAfterWrite.Load())
	if f.loseReply.Load() {
		f.wrongRead.Store(true)
		panic(http.ErrAbortHandler)
	}
	switch f.vendor {
	case provider.TencentCVM:
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]string{"RequestId": volumeResizeReceipt}})
	case provider.AlibabaECS:
		testsupport.WriteJSON(t, w, map[string]string{"RequestId": volumeResizeReceipt})
	case provider.HuaweiECS:
		w.WriteHeader(http.StatusAccepted)
		testsupport.WriteJSON(t, w, map[string]string{"job_id": volumeResizeReceipt})
	}
}

func (f *volumeResizeFixture) diskRow() map[string]any {
	id, state := f.id, f.available
	if f.wrongRead.Load() {
		id += "-wrong"
	}
	if f.pending.Load() {
		state = f.resizing
	}
	size := f.size.Load()
	switch f.vendor {
	case provider.TencentCVM:
		billing := "POSTPAID_BY_HOUR"
		if f.prepaid.Load() {
			billing = "PREPAID"
		}
		return map[string]any{"DiskId": id, "DiskState": state, "DiskSize": size, "DiskUsage": "DATA_DISK",
			"DiskChargeType": billing, "Attached": false, "Shareable": false, "Portable": true}
	case provider.AlibabaECS:
		billing := "PostPaid"
		if f.prepaid.Load() {
			billing = "PrePaid"
		}
		return map[string]any{"DiskId": id, "RegionId": f.region, "Status": state, "Type": "data", "Size": size,
			"Portable": true, "MultiAttach": "Disabled", "Category": "cloud_essd", "DiskChargeType": billing}
	default:
		meta := map[string]string{}
		if f.prepaid.Load() {
			meta["orderID"] = "fixture-subscription"
		}
		return map[string]any{"id": id, "status": state, "size": size, "bootable": "false", "multiattach": false,
			"os-vol-tenant-attr:tenant_id": fixtureHuaweiProject, "attachments": []any{}, "metadata": meta}
	}
}
