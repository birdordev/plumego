package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const volumeReleaseReceipt = "fixture-release-volume"

type volumeReleaseFixture struct {
	volumeResizeFixture
	snapshotPresent, settled atomic.Bool
	reads                    atomic.Int32
}

func (f *volumeReleaseFixture) serveRelease(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.serve(t, w, r)
		return
	}
	var valid bool
	switch f.vendor {
	case provider.TencentCVM:
		valid = f.serveTencentRelease(t, w, r)
	case provider.AlibabaECS:
		valid = f.serveAlibabaRelease(t, w, r)
	default:
		valid = f.serveHuaweiRelease(t, w, r)
	}
	if !valid {
		t.Errorf("unexpected release request %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *volumeReleaseFixture) snapshotRows() []any {
	rows := []any{}
	if f.snapshotPresent.Load() {
		rows = append(rows, map[string]string{"id": f.snapshot})
	}
	return rows
}

func (f *volumeReleaseFixture) releaseLookupStatus() int {
	if f.settled.Load() {
		return http.StatusOK
	}
	switch f.reads.Add(1) {
	case 1:
		return http.StatusOK
	case 2:
		return http.StatusTooManyRequests
	default:
		return http.StatusOK
	}
}

func (f *volumeReleaseFixture) serveTencentRelease(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	data, err := io.ReadAll(r.Body)
	var request struct {
		DiskIds        []string
		DeleteSnapshot *int
		Filters        []struct {
			Name   string
			Values []string
		}
	}
	if err != nil || json.Unmarshal(data, &request) != nil || r.Method != http.MethodPost || r.Host != "cbs.tencentcloudapi.com" ||
		r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, data, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	switch r.Header.Get("X-TC-Action") {
	case "DescribeSnapshots":
		if len(request.Filters) != 1 || request.Filters[0].Name != "disk-id" ||
			len(request.Filters[0].Values) != 1 || request.Filters[0].Values[0] != f.id {
			return false
		}
		rows := f.snapshotRows()
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": len(rows), "SnapshotSet": rows}})
	case "TerminateDisks":
		if len(request.DiskIds) != 1 || request.DiskIds[0] != f.id ||
			request.DeleteSnapshot == nil || *request.DeleteSnapshot != 0 || f.snapshotPresent.Load() {
			return false
		}
		f.sendCount.Add(1)
		panic(http.ErrAbortHandler)
	case "DescribeDisks":
		if len(request.DiskIds) != 1 || request.DiskIds[0] != f.id {
			return false
		}
		rows := []any{f.diskRow()}
		if f.sendCount.Load() != 0 {
			if status := f.releaseLookupStatus(); status != http.StatusOK {
				w.WriteHeader(status)
				return true
			}
			rows = []any{}
			if !f.settled.Load() && f.reads.Load() > 3 {
				testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]int{"TotalCount": 0}})
				return true
			}
		}
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"DiskSet": rows, "TotalCount": len(rows)}})
	default:
		return false
	}
	return true
}

func (f *volumeReleaseFixture) serveAlibabaRelease(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	query := r.URL.Query()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Method != http.MethodPost || r.URL.Path != "/" ||
		r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	switch r.Header.Get("X-ACS-Action") {
	case "DescribeSnapshots":
		if query.Get("RegionId") != f.region || query.Get("DiskId") != f.id {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"Snapshots": map[string]any{"Snapshot": f.snapshotRows()}})
	case "DeleteDisk":
		if len(query) != 1 || query.Get("DiskId") != f.id || f.snapshotPresent.Load() {
			return false
		}
		f.sendCount.Add(1)
		panic(http.ErrAbortHandler)
	case "DescribeDisks":
		if query.Get("RegionId") != f.region || query.Get("DiskIds") != `["`+f.id+`"]` {
			return false
		}
		row := f.diskRow()
		row["DeleteAutoSnapshot"] = false
		rows := []any{row}
		if f.sendCount.Load() != 0 {
			if status := f.releaseLookupStatus(); status != http.StatusOK {
				w.WriteHeader(status)
				return true
			}
			rows = []any{}
			if !f.settled.Load() && f.reads.Load() > 3 {
				testsupport.WriteJSON(t, w, map[string]any{"Disks": map[string]any{"Disk": rows}, "NextToken": "unexpected"})
				return true
			}
		}
		testsupport.WriteJSON(t, w, map[string]any{"Disks": map[string]any{"Disk": rows}})
	default:
		return false
	}
	return true
}

func (f *volumeReleaseFixture) serveHuaweiRelease(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Host != "evs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	path := "/v2/" + fixtureHuaweiProject + "/cloudvolumes"
	query := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/"+fixtureHuaweiProject+"/cloudsnapshots/detail":
		if query.Get("volume_id") != f.id || query.Get("enterprise_project_id") != "all_granted_eps" {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"count": 0, "snapshots": []any{}})
	case r.Method == http.MethodGet && r.URL.Path == "/v5/"+fixtureHuaweiProject+"/snapshots/detail":
		if query.Get("volume_id") != f.id || query.Has("enterprise_project_id") {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"snapshots": f.snapshotRows()})
	case r.Method == http.MethodGet && r.URL.Path == path+"/"+f.id:
		testsupport.WriteJSON(t, w, map[string]any{"volume": f.diskRow()})
	case r.Method == http.MethodDelete && r.URL.Path == path+"/"+f.id:
		if f.snapshotPresent.Load() {
			return false
		}
		f.sendCount.Add(1)
		testsupport.WriteJSON(t, w, map[string]string{"job_id": volumeReleaseReceipt})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/jobs/"+volumeReleaseReceipt:
		f.jobReads.Add(1)
		testsupport.WriteJSON(t, w, map[string]any{"job_id": volumeReleaseReceipt, "job_type": "deleteVolume", "status": "SUCCESS",
			"entities": map[string]string{"volume_id": f.id}})
	case r.Method == http.MethodGet && r.URL.Path == path+"/detail":
		if query.Get("id") != f.id || query.Get("enterprise_project_id") != "all_granted_eps" || f.sendCount.Load() == 0 {
			return false
		}
		if status := f.releaseLookupStatus(); status != http.StatusOK {
			w.WriteHeader(status)
			return true
		}
		if !f.settled.Load() && f.reads.Load() > 3 {
			testsupport.WriteJSON(t, w, map[string]int{"count": 0})
			return true
		}
		testsupport.WriteJSON(t, w, map[string]any{"count": 0, "volumes": []any{}})
	default:
		return false
	}
	return true
}
