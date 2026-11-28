package cloudtest

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const (
	recoverySource = "97000000000040008000000000000001in01"
	recoveryTarget = "97000000000040008000000000000002in01"
	recoveryJob    = "f495fb34-54be-441e-a276-35198404a177"
	recoveryKeyID  = "1039c7c7-c365-446e-88fa-50f8a682afd7"
	recoveryVPC    = "70000000-0000-4000-8000-000000000012"
	recoverySubnet = "70000000-0000-4000-8000-000000000013"
	recoveryGroup  = "70000000-0000-4000-8000-000000000014"
	recoveryName   = "fixture-recovered"
)

type databaseRecoveryFixture struct {
	databaseFixture
	point               time.Time
	placement           provider.DatabaseRecoveryPlacement
	mode, writes, reads atomic.Int32
}

func (f *databaseRecoveryFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}
	prefix := "/v3/" + fixtureHuaweiProject
	if r.Host != "rds."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		t.Error("recovery request escaped its verified Huawei project")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/instances/"+recoverySource+"/restore-time" && len(q) == 0:
		windows := []map[string]int64{{"start_time": f.point.Add(-time.Minute).UnixMilli(), "end_time": f.point.UnixMilli()},
			{"start_time": f.point.Add(time.Minute).UnixMilli(), "end_time": f.point.Add(2 * time.Minute).UnixMilli()}}
		if f.mode.Load() == 1 {
			windows = windows[1:]
		}
		testsupport.WriteJSON(t, w, map[string]any{"restore_time": windows})
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/instances" && q.Get("limit") == "50" && q.Get("offset") == "0" &&
		(q.Get("id") == recoverySource || q.Get("id") == recoveryTarget):
		row := f.row(q.Get("id"))
		testsupport.WriteJSON(t, w, map[string]any{"total_count": 1, "instances": []any{row}})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"/instances" && len(q) == 0:
		f.create(t, w, r)
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/flavors/mysql" && len(q) == 2 &&
		q.Get("version_name") == "8.0" && q.Get("spec_code") == f.placement.Flavor:
		status := "normal"
		if f.mode.Load() == 4 {
			status = "sellout"
		}
		testsupport.WriteJSON(t, w, map[string]any{"flavors": []any{map[string]any{
			"spec_code": f.placement.Flavor, "instance_mode": "single", "az_status": map[string]string{f.placement.Zone: status},
		}}})
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/jobs" && len(q) == 1 && q.Get("id") == recoveryJob:
		f.reads.Add(1)
		id := recoveryTarget
		if f.mode.Load() == 2 {
			id = recoverySource
		}
		testsupport.WriteJSON(t, w, map[string]any{"job": map[string]any{"id": recoveryJob, "status": "Completed",
			"instance": map[string]string{"id": id, "name": recoveryName}}})
	default:
		t.Errorf("unexpected recovery request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *databaseRecoveryFixture) row(id string) map[string]any {
	row := f.databaseRow(provider.HuaweiRDS, id, "source-instance", "8.0", f.region)
	row["status"] = "ACTIVE"
	row["disk_encryption_id"] = recoveryKeyID
	row["volume"] = map[string]any{"size": 30, "type": "CLOUDSSD"}
	if id == recoveryTarget {
		row["name"], row["vpc_id"], row["subnet_id"], row["security_group_id"] = recoveryName, recoveryVPC, recoverySubnet, recoveryGroup
		row["flavor_ref"] = f.placement.Flavor
		row["volume"] = map[string]any{"size": f.placement.StorageGiB, "type": f.placement.StorageType}
		row["public_ips"] = []string{}
		row["nodes"] = []any{map[string]string{"availability_zone": f.placement.Zone}}
	}
	return row
}

func (f *databaseRecoveryFixture) create(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var got map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil || len(got) != 10 {
		t.Error("invalid recovery request body")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for key, want := range map[string]any{
		"name": recoveryName, "region": f.region, "vpc_id": recoveryVPC, "subnet_id": recoverySubnet, "security_group_id": recoveryGroup,
		"availability_zone": f.placement.Zone, "flavor_ref": f.placement.Flavor, "disk_encryption_id": recoveryKeyID,
	} {
		var value any
		if err := json.Unmarshal(got[key], &value); err != nil || value != want {
			t.Errorf("recovery payload did not pin %s", key)
		}
	}
	var point struct {
		Source string `json:"instance_id"`
		Type   string
		Time   int64 `json:"restore_time"`
	}
	var volume struct {
		Type string
		Size int
	}
	if json.Unmarshal(got["restore_point"], &point) != nil || json.Unmarshal(got["volume"], &volume) != nil ||
		point.Source != recoverySource || point.Type != "timestamp" || point.Time != f.point.UnixMilli() ||
		volume.Type != f.placement.StorageType || volume.Size != f.placement.StorageGiB {
		t.Error("recovery payload lost its approved restore point or disk settings")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.writes.Add(1)
	if f.mode.Load() == 3 {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	testsupport.WriteJSON(t, w, map[string]any{"job_id": recoveryJob,
		"instance": map[string]string{"id": recoveryTarget, "name": recoveryName, "status": "BUILD"}})
}
