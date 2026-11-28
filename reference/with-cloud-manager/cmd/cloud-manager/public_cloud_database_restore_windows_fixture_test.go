package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

func (f *databaseBackupsFixture) restoreAction() (service, action, version string) {
	switch f.product {
	case provider.TencentCDB:
		return "cdb", "DescribeRollbackRangeTime", "2017-03-20"
	case provider.TencentPostgreSQL:
		return "postgres", "DescribeAvailableRecoveryTime", "2017-03-12"
	case provider.TencentSQLServer:
		return "sqlserver", "DescribeRestoreTimeRange", "2018-03-28"
	default:
		return "", "", ""
	}
}

func (f *databaseBackupsFixture) serveRestoreWindows(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	service, action, version := f.restoreAction()
	var valid bool
	switch {
	case action != "" && r.Header.Get("X-TC-Action") == action:
		valid = f.validTencentRestoreRequest(r, service, version)
	case r.Header.Get("X-ACS-Action") == "DescribeLocalAvailableRecoveryTime":
		q := r.URL.Query()
		valid = f.product == provider.AlibabaRDS && r.Method == http.MethodPost && r.Host == "rds.aliyuncs.com" &&
			r.Header.Get("X-ACS-Version") == "2014-08-15" && r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) &&
			len(q) == 2 && q.Get("DBInstanceId") == f.id && q.Get("Region") == f.region
	case strings.HasSuffix(r.URL.Path, "/restore-time"):
		valid = f.product == provider.HuaweiRDS && r.Method == http.MethodGet && r.Host == "rds."+f.region+".myhuaweicloud.com" &&
			r.URL.Path == "/v3/"+fixtureHuaweiProject+"/instances/"+f.id+"/restore-time" &&
			r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM && r.URL.RawQuery == ""
	default:
		return false
	}
	if !valid {
		t.Error("invalid native restore request scope or authorization")
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	mode := f.restoreMode.Load()
	if mode == 1 {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	if mode == 5 {
		f.restoreParentDrift.Store(true)
	}
	result := f.restoreResponse(mode)
	if f.vendor == provider.TencentCVM {
		result = map[string]any{"Response": result}
	}
	writeCloudFixtureJSON(t, w, result)
	return true
}

func (f *databaseBackupsFixture) validTencentRestoreRequest(r *http.Request, service, version string) bool {
	body, err := io.ReadAll(r.Body)
	var req map[string]json.RawMessage
	if err != nil || json.Unmarshal(body, &req) != nil || len(req) != 1 || r.Method != http.MethodPost ||
		r.Host != service+".tencentcloudapi.com" || r.Header.Get("X-TC-Version") != version ||
		r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
		!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	switch f.product {
	case provider.TencentCDB:
		var ids []string
		return json.Unmarshal(req["InstanceIds"], &ids) == nil && len(ids) == 1 && ids[0] == f.id
	case provider.TencentPostgreSQL:
		var id string
		return json.Unmarshal(req["DBInstanceId"], &id) == nil && id == f.id
	default:
		var id string
		return json.Unmarshal(req["InstanceId"], &id) == nil && id == f.id
	}
}

func (f *databaseBackupsFixture) restoreResponse(mode int32) map[string]any {
	start, end := f.start, f.start.Add(time.Minute)
	if mode == 3 {
		start, end = end, end.Add(time.Minute)
	}
	china := time.FixedZone("fixture-cst", 8*60*60)
	switch f.product {
	case provider.TencentCDB:
		row := map[string]any{"InstanceId": f.id, "Code": 0, "Message": backupFixtureURL}
		times := []map[string]string{
			{"Begin": start.Add(2 * time.Minute).In(china).Format(time.DateTime), "End": end.Add(2 * time.Minute).In(china).Format(time.DateTime)},
			{"Begin": start.In(china).Format(time.DateTime), "End": end.In(china).Format(time.DateTime)},
		}
		switch mode {
		case 2:
			row["InstanceId"] = "cdb-outside"
		case 3:
			times = times[:0]
		}
		row["Times"] = times
		return map[string]any{"TotalCount": 1, "Items": []any{row}}
	case provider.TencentPostgreSQL:
		if mode == 2 {
			return map[string]any{"RecoveryEndTime": end.In(china).Format(time.DateTime)}
		}
		return map[string]any{"RecoveryBeginTime": start.In(china).Format(time.DateTime), "RecoveryEndTime": end.In(china).Format(time.DateTime)}
	case provider.TencentSQLServer:
		if mode == 2 {
			start, end = end, start
		}
		return map[string]any{"MinTime": start.Format(time.DateTime), "MaxTime": end.Format(time.DateTime)}
	case provider.AlibabaRDS:
		id := f.id
		if mode == 2 {
			id = "rm-outside"
		}
		return map[string]any{"DBInstanceId": id, "RecoveryBeginTime": start.Format(time.RFC3339), "RecoveryEndTime": end.Format(time.RFC3339)}
	default:
		windows := []map[string]int64{
			{"start_time": start.Add(2*time.Minute + time.Millisecond).UnixMilli(), "end_time": end.Add(2 * time.Minute).UnixMilli()},
			{"start_time": start.UnixMilli(), "end_time": end.UnixMilli()},
		}
		switch mode {
		case 2:
			windows[0]["start_time"] = 0
		case 3:
			windows = windows[:0]
		case 4:
			for len(windows) <= provider.MaxDatabaseRestoreWindows {
				windows = append(windows, windows[0])
			}
		}
		return map[string]any{"restore_time": windows}
	}
}
