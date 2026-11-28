package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

const backupFixtureURL = "https://fixture-download.invalid/backup?signature=fixture-only"

type databaseBackupsFixture struct {
	*databaseFixture
	mode, restoreMode  atomic.Int32
	restoreParentDrift atomic.Bool
	start              time.Time
}

func (f *databaseBackupsFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if f.serveRestoreWindows(t, w, r) {
		return
	}
	action := r.Header.Get("X-TC-Action")
	if action != "DescribeBackups" && action != "DescribeBaseBackups" && r.Header.Get("X-ACS-Action") != "DescribeBackups" &&
		!strings.HasSuffix(r.URL.Path, "/backups") {
		f.serveDatabase(t, w, r)
		return
	}
	offset, valid := f.backupRequest(r)
	if !valid || (offset != 0 && offset != 100) {
		t.Error("unexpected backup request scope, pagination or authorization")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	mode := f.mode.Load()
	if offset == 100 && mode == 1 {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	total := 101
	if mode == 3 {
		total = 0
	}
	items := make([]map[string]any, 0, 100)
	for i := offset; i < min(offset+100, total); i++ {
		id := i + 1
		if i == 100 && mode == 2 {
			id = 1
		}
		items = append(items, f.backupRow(id))
	}
	switch f.product {
	case provider.AlibabaRDS:
		writeCloudFixtureJSON(t, w, map[string]any{"Items": map[string]any{"Backup": items}, "TotalRecordCount": strconv.Itoa(total),
			"PageNumber": strconv.Itoa(offset/100 + 1), "PageRecordCount": strconv.Itoa(len(items))})
	case provider.HuaweiRDS:
		writeCloudFixtureJSON(t, w, map[string]any{"backups": items, "total_count": total})
	default:
		field := map[string]string{provider.TencentCDB: "Items", provider.TencentPostgreSQL: "BaseBackupSet", provider.TencentSQLServer: "Backups"}[f.product]
		writeCloudFixtureJSON(t, w, map[string]any{"Response": map[string]any{field: items, "TotalCount": total}})
	}
}

func (f *databaseBackupsFixture) backupRequest(r *http.Request) (int, bool) {
	q := r.URL.Query()
	switch f.vendor {
	case provider.TencentCVM:
		body, err := io.ReadAll(r.Body)
		var req struct {
			InstanceId, StartTime, EndTime, MinFinishTime, MaxFinishTime, MinStartTime, MaxStartTime string
			Offset, Limit, Group, Type                                                               int
			Filters                                                                                  []struct {
				Name   string
				Values []string
			}
		}
		valid := err == nil && json.Unmarshal(body, &req) == nil &&
			r.Method == http.MethodPost && req.Limit == 100 && r.Header.Get("X-TC-Region") == f.region &&
			r.Header.Get("X-TC-Token") == fixtureLaunchSession && validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret)
		switch f.product {
		case provider.TencentPostgreSQL:
			return req.Offset, valid && r.Host == "postgres.tencentcloudapi.com" && r.Header.Get("X-TC-Version") == "2017-03-12" &&
				r.Header.Get("X-TC-Action") == "DescribeBaseBackups" && len(req.Filters) == 1 && req.Filters[0].Name == "db-instance-id" &&
				len(req.Filters[0].Values) == 1 && req.Filters[0].Values[0] == f.id && req.MinFinishTime < req.MaxFinishTime
		case provider.TencentSQLServer:
			return req.Offset * 100, valid && r.Host == "sqlserver.tencentcloudapi.com" && r.Header.Get("X-TC-Version") == "2018-03-28" &&
				r.Header.Get("X-TC-Action") == "DescribeBackups" && req.StartTime < req.EndTime && req.Group == 1 && req.Type == 1 && req.InstanceId == f.id
		default:
			return req.Offset, valid && r.Host == "cdb.tencentcloudapi.com" && r.Header.Get("X-TC-Version") == "2017-03-20" &&
				r.Header.Get("X-TC-Action") == "DescribeBackups" && req.InstanceId == f.id && req.MinStartTime < req.MaxStartTime
		}
	case provider.AlibabaECS:
		page, err := strconv.Atoi(q.Get("PageNumber"))
		return (page - 1) * 100, err == nil && r.Host == "rds.aliyuncs.com" && r.Method == http.MethodPost &&
			r.Header.Get("X-ACS-Version") == "2014-08-15" && r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) && q.Get("DBInstanceId") == f.id &&
			q.Get("PageSize") == "100" && q.Get("StartTime") < q.Get("EndTime")
	default:
		offset, err := strconv.Atoi(q.Get("offset"))
		return offset, err == nil && r.Host == "rds."+f.region+".myhuaweicloud.com" && r.Method == http.MethodGet &&
			r.URL.Path == "/v3/"+fixtureHuaweiProject+"/backups" && r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM &&
			q.Get("instance_id") == f.id && q.Get("limit") == "100" && q.Get("begin_time") < q.Get("end_time")
	}
}

func (f *databaseBackupsFixture) backupRow(id int) map[string]any {
	start := f.start
	end := start.Add(time.Minute)
	sqlStart := start.Add(8 * time.Hour).Format(time.DateTime)
	sqlEnd := end.Add(8 * time.Hour).Format(time.DateTime)
	switch f.product {
	case provider.TencentCDB:
		return map[string]any{"BackupId": id, "StartTime": sqlStart, "FinishTime": sqlEnd, "Status": "SUCCESS",
			"Way": "automatic", "Method": "full", "Size": 4096, "InternetUrl": backupFixtureURL}
	case provider.TencentPostgreSQL:
		return map[string]any{"Id": fmt.Sprintf("postgres-%03d", id), "DBInstanceId": f.id, "StartTime": sqlStart,
			"FinishTime": sqlEnd, "State": "finished", "Size": 4096, "BackupMode": "manual", "InternalUrl": backupFixtureURL}
	case provider.TencentSQLServer:
		return map[string]any{"Id": 0, "GroupId": strconv.Itoa(id), "StartTime": start.Format(time.DateTime), "EndTime": end.Format(time.DateTime),
			"Status": 1, "BackupWay": 0, "Size": 4, "ExternalAddr": backupFixtureURL}
	case provider.AlibabaRDS:
		return map[string]any{"BackupId": strconv.Itoa(id), "DBInstanceId": f.id, "BackupStartTime": start.Format(time.RFC3339),
			"BackupEndTime": end.Format(time.RFC3339), "BackupSize": 4096, "BackupStatus": "Success", "BackupMode": "Manual",
			"BackupType": "FullBackup", "Engine": "MySQL", "BackupDownloadURL": backupFixtureURL}
	default:
		return map[string]any{"id": fmt.Sprintf("hua-%03d", id), "instance_id": f.id, "begin_time": start.Format("2006-01-02T15:04:05-0700"),
			"end_time": end.Format("2006-01-02T15:04:05-0700"), "size": 4, "status": "COMPLETED", "type": "manual",
			"datastore": map[string]string{"type": "MySQL"}, "name": "hidden-database-backup-name", "download_link": backupFixtureURL}
	}
}
