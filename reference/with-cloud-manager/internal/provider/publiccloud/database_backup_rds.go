package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type alibabaBackup struct {
	BackupId                                                   json.Number
	DBInstanceId, Engine, BackupType, BackupMode, BackupStatus string
	BackupStartTime, BackupEndTime                             string
	BackupSize                                                 *int64
}

func (c *Client) alibabaBackupPage(ctx context.Context, query databaseBackupQuery, offset int) (databaseBackupPage, error) {
	const minute = "2006-01-02T15:04Z"
	number := offset/databaseBackupPageSize + 1
	params := url.Values{
		"DBInstanceId": {query.id}, "StartTime": {query.start.UTC().Format(minute)}, "EndTime": {query.end.UTC().Format(minute)},
		"PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(databaseBackupPageSize)},
	}
	var result struct {
		Items                                         struct{ Backup []alibabaBackup }
		PageNumber, PageRecordCount, TotalRecordCount json.Number
	}
	if err := c.alibaba(ctx, alibabaRDSEndpoint(query.region), "DescribeBackups", "2014-08-15", params, &result); err != nil {
		return databaseBackupPage{}, err
	}
	total, err := strconv.Atoi(result.TotalRecordCount.String())
	if err != nil || result.PageNumber.String() != strconv.Itoa(number) ||
		result.PageRecordCount.String() != strconv.Itoa(len(result.Items.Backup)) {
		return databaseBackupPage{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return projectDatabaseBackups(result.Items.Backup, &total, func(row alibabaBackup) (provider.DatabaseBackup, error) {
		if row.DBInstanceId != query.id || (row.Engine != "" && !strings.EqualFold(row.Engine, query.details.Engine)) {
			return provider.DatabaseBackup{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return makeDatabaseBackup(row.BackupId.String(), backupKind(row.BackupType), backupMode(row.BackupMode), backupStatus(row.BackupStatus),
			row.BackupStartTime, row.BackupEndTime, row.BackupSize, time.UTC)
	})
}

type huaweiBackup struct {
	ID, Type, Status string
	Instance         string `json:"instance_id"`
	Start            string `json:"begin_time"`
	End              string `json:"end_time"`
	Size             *int64
	Datastore        struct{ Type string }
}

func (c *Client) huaweiBackupPage(ctx context.Context, query databaseBackupQuery, offset int) (databaseBackupPage, error) {
	const stamp = "2006-01-02T15:04:05-0700"
	params := url.Values{
		"instance_id": {query.id}, "offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(databaseBackupPageSize)},
		"begin_time": {query.start.UTC().Format(stamp)}, "end_time": {query.end.UTC().Format(stamp)},
	}
	endpoint := url.URL{Scheme: "https", Host: "rds." + query.region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/backups", RawQuery: params.Encode()}
	var result struct {
		Backups []huaweiBackup
		Total   *int `json:"total_count"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return databaseBackupPage{}, err
	}
	return projectDatabaseBackups(result.Backups, result.Total, func(row huaweiBackup) (provider.DatabaseBackup, error) {
		if row.Instance != query.id || !strings.EqualFold(row.Datastore.Type, query.details.Engine) {
			return provider.DatabaseBackup{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		bytes, err := backupBytesFromKiB(row.Size)
		if err != nil {
			return provider.DatabaseBackup{}, err
		}
		kind := backupKind(row.Type)
		if row.Type == "auto" || row.Type == "manual" {
			kind = "full"
		}
		mode := backupMode(row.Type)
		if row.Type == "incremental" {
			mode = "automatic"
		}
		return makeDatabaseBackup(row.ID, kind, mode, backupStatus(row.Status), row.Start, row.End, bytes, time.UTC)
	})
}
